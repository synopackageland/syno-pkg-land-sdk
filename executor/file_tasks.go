package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"time"
)

// FileTasks starts and follows File Station background tasks (copy, move and
// delete) as the delegated user. Whether a task started by one synowebapi
// process can be followed from another is a DSM property; a task DSM no longer
// knows reports state "unknown" and the App verifies the result with files.info.
type FileTasks interface {
	Start(ctx context.Context, runner, kind string, params map[string]any) (string, error)
	Status(ctx context.Context, runner, kind, id string) (taskProgress, error)
	Stop(ctx context.Context, runner, kind, id string) error
	// RecycleBin reports whether deletes in scope go to the recycle bin.
	RecycleBin(ctx context.Context, s Subject, scope string) (bool, error)
}

// taskProgress is one status answer. Paths are every path DSM reports.
type taskProgress struct {
	Finished bool
	Progress float64
	Paths    []string
	Code     int
}

type fileTask struct {
	ID, DSMID, Kind, Delegation, Job, Runner, Path string
	Done                                           bool
	// State, Error and Progress are the terminal answer once the task ended.
	State, Error string
	Progress     float64
}

const maxFileTasks = 64

// dsmNoSuchTask is File Station's "no such task of the file operation".
const dsmNoSuchTask = 599

var taskOps = []string{"files.taskStatus", "files.taskCancel"}
var taskKinds = map[string]string{"files.copy": "copy", "files.move": "move", "files.delete": "delete"}

// startTask checks a copy, move or delete request and starts its task.
func (e *Executor) startTask(ctx context.Context, op string, d Delegation, s Subject, p Params, hasConflict bool) Response {
	if e.Tasks == nil {
		return deny("DSM_FAILED")
	}
	if p.Path == d.Path {
		// The granted folder itself is never copied, moved or deleted.
		return deny("SCOPE_DENIED")
	}
	params := map[string]any{"path": p.Path}
	if op == "files.delete" {
		info, err := e.DSM.API(ctx, s.Username, "files.info", map[string]any{"path": p.Path})
		if err != nil {
			return deny(fileStationCode(err))
		}
		var out struct {
			Files []struct {
				Path  string `json:"path"`
				IsDir bool   `json:"isdir"`
			} `json:"files"`
		}
		if json.Unmarshal(info, &out) != nil || len(out.Files) != 1 || out.Files[0].Path != p.Path {
			return deny("DSM_FAILED")
		}
		if out.Files[0].IsDir && !p.Recursive {
			return deny("recursive-required")
		}
		recycle, err := e.Tasks.RecycleBin(ctx, s, d.Scope)
		if err != nil {
			return deny("DSM_FAILED")
		}
		if !recycle && !p.Permanent {
			return deny("recycle-bin-disabled")
		}
		params["recursive"] = p.Recursive
	} else {
		if hasConflict && p.Conflict != "fail" && p.Conflict != conflictOverwrite {
			return deny("INVALID_REQUEST")
		}
		if !cleanPath(p.Dest) {
			return deny("INVALID_REQUEST")
		}
		if !within(d.Path, p.Dest) {
			return deny("SCOPE_DENIED")
		}
		if _, err := scopedPath(d, p.Dest); err != nil {
			return deny(scopeCode(err))
		}
		if within(p.Path, p.Dest) {
			return deny("INVALID_REQUEST")
		}
		params["dest_dir"] = p.Dest
		if p.Conflict == conflictOverwrite {
			params["overwrite"] = true
		}
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return deny("INTERNAL")
	}
	task := &fileTask{ID: hex.EncodeToString(id), Kind: taskKinds[op], Delegation: d.ID, Job: d.Job, Runner: s.Username, Path: p.Path}
	if !e.reserveTask(task) {
		return deny("too-many-tasks")
	}
	done, code := e.beginLegacy(d)
	if code != "" {
		e.dropTask(task.ID)
		return deny(code)
	}
	defer done()
	dsmID, err := e.Tasks.Start(ctx, s.Username, task.Kind, params)
	if err != nil || dsmID == "" {
		e.dropTask(task.ID)
		return deny(fileStationCode(err))
	}
	e.taskMu.Lock()
	task.DSMID = dsmID
	e.taskMu.Unlock()
	return success(map[string]any{"task_id": task.ID, "state": "running"})
}

// followTask answers files.taskStatus and files.taskCancel for a task of d.
func (e *Executor) followTask(ctx context.Context, op string, d Delegation, p Params) Response {
	e.taskMu.Lock()
	found := e.tasks[p.TaskID]
	var task fileTask
	if found != nil {
		task = *found
	}
	e.taskMu.Unlock()
	if found == nil || task.DSMID == "" || task.Delegation != d.ID || task.Job != d.Job || e.Tasks == nil {
		return deny("task-not-found")
	}
	if task.State != "" {
		// DSM forgets a task after reporting its end; the answer stays here.
		return success(terminalStatus(task))
	}
	if op == "files.taskCancel" {
		if err := e.Tasks.Stop(ctx, task.Runner, task.Kind, task.DSMID); err != nil {
			return deny(fileStationCode(err))
		}
		task.State = "canceled"
		e.settleTask(task)
		return success(terminalStatus(task))
	}
	progress, err := e.Tasks.Status(ctx, task.Runner, task.Kind, task.DSMID)
	var refused *dsmError
	if errors.As(err, &refused) && refused.Code == dsmNoSuchTask {
		return success(map[string]any{"task_id": task.ID, "state": "unknown"})
	}
	if err != nil {
		return deny(fileStationCode(err))
	}
	for _, reported := range progress.Paths {
		if !cleanPath(reported) || !within(d.Path, reported) {
			e.Tasks.Stop(ctx, task.Runner, task.Kind, task.DSMID)
			e.finishTask(task.ID)
			return deny("SCOPE_DENIED")
		}
	}
	task.Progress = fraction(progress.Progress)
	if !progress.Finished && progress.Code == 0 {
		return success(map[string]any{"task_id": task.ID, "state": "running", "progress": task.Progress})
	}
	task.State = "finished"
	if progress.Code != 0 {
		task.State, task.Error = "failed", fileStationCode(&dsmError{Code: progress.Code})
		// A delete the ACL refused lists only the generic 900; the refused
		// folder is the cause, anything else stays DSM_FAILED.
		if task.Kind == "delete" && progress.Code == dsmDeleteFailed && e.deleteRemains(ctx, task) == "PERMISSION_DENIED" {
			task.Error = "PERMISSION_DENIED"
		}
	} else if task.Kind == "delete" {
		if code := e.deleteRemains(ctx, task); code != "" {
			task.State, task.Error = "failed", code
		}
	}
	e.settleTask(task)
	return success(terminalStatus(task))
}

// deleteRemains is the code for a delete that ended with its path still in
// place, which DSM can report without listing a cause; "" when it is gone.
func (e *Executor) deleteRemains(ctx context.Context, task fileTask) string {
	_, err := e.DSM.API(ctx, task.Runner, "files.info", map[string]any{"path": task.Path})
	if err != nil {
		if code := fileStationCode(err); code != "not-found" {
			return code
		}
		return ""
	}
	if _, err = e.DSM.API(ctx, task.Runner, "files.checkPermission", map[string]any{"path": filepath.Dir(task.Path)}); err != nil {
		return "PERMISSION_DENIED"
	}
	return "DSM_FAILED"
}

func terminalStatus(task fileTask) map[string]any {
	out := map[string]any{"task_id": task.ID, "state": task.State, "progress": task.Progress}
	if task.State == "finished" {
		out["progress"] = 1.0
	}
	if task.Error != "" {
		out["error"] = task.Error
	}
	return out
}

// fraction is File Station's progress as 0 to 1; it reports -1 before it
// has measured the task.
func fraction(p float64) float64 {
	return math.Max(0, math.Min(1, p))
}

// reserveTask admits a task record, evicting finished ones when full.
func (e *Executor) reserveTask(task *fileTask) bool {
	e.taskMu.Lock()
	defer e.taskMu.Unlock()
	if e.tasks == nil {
		e.tasks = map[string]*fileTask{}
	}
	if len(e.tasks) >= maxFileTasks {
		for id, t := range e.tasks {
			if t.Done {
				delete(e.tasks, id)
			}
		}
	}
	if len(e.tasks) >= maxFileTasks {
		return false
	}
	e.tasks[task.ID] = task
	return true
}
func (e *Executor) dropTask(id string) {
	e.taskMu.Lock()
	defer e.taskMu.Unlock()
	delete(e.tasks, id)
}
func (e *Executor) settleTask(task fileTask) {
	e.taskMu.Lock()
	defer e.taskMu.Unlock()
	if t := e.tasks[task.ID]; t != nil {
		t.Done, t.State, t.Error, t.Progress = true, task.State, task.Error, task.Progress
	}
}
func (e *Executor) finishTask(id string) {
	e.taskMu.Lock()
	defer e.taskMu.Unlock()
	if t := e.tasks[id]; t != nil {
		t.Done = true
	}
}

// stopTasks stops the running tasks of a revoked delegation, best effort.
func (e *Executor) stopTasks(delegation string) {
	e.taskMu.Lock()
	var running []fileTask
	for _, t := range e.tasks {
		if t.Delegation == delegation && !t.Done && t.DSMID != "" {
			running = append(running, *t)
			t.Done = true
		}
	}
	e.taskMu.Unlock()
	if e.Tasks == nil {
		return
	}
	for _, t := range running {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		e.Tasks.Stop(ctx, t.Runner, t.Kind, t.DSMID)
		cancel()
	}
}
