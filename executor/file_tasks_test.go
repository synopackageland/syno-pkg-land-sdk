package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeTasks struct {
	mu       sync.Mutex
	started  []startedTask
	stopped  []string
	progress taskProgress
	err      error
	recycle  bool
}
type startedTask struct {
	runner, kind string
	params       map[string]any
}

func (f *fakeTasks) Start(_ context.Context, runner, kind string, params map[string]any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, startedTask{runner, kind, params})
	return "FileStation_task", nil
}
func (f *fakeTasks) Status(context.Context, string, string, string) (taskProgress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.progress, f.err
}
func (f *fakeTasks) Stop(_ context.Context, _, _, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	return nil
}
func (f *fakeTasks) RecycleBin(context.Context, Subject, string) (bool, error) {
	return f.recycle, nil
}

func taskSetup(t *testing.T, ops ...string) (*Executor, *fakeDSM, *fakeTasks, string) {
	t.Helper()
	e, f, _ := setup(t)
	ft := &fakeTasks{recycle: true}
	e.Tasks = ft
	for _, name := range []string{"src", "dst", "src/inner"} {
		os.MkdirAll(filepath.Join(f.root, name), 0700)
	}
	os.WriteFile(filepath.Join(f.root, "a.txt"), nil, 0600)
	return e, f, ft, grant(t, e, ops...)
}
func taskID(t *testing.T, r Response) string {
	t.Helper()
	b, _ := json.Marshal(r.Result)
	var out struct {
		TaskID string `json:"task_id"`
		State  string `json:"state"`
	}
	json.Unmarshal(b, &out)
	if !r.OK || !idRE.MatchString(out.TaskID) || out.State != "running" {
		t.Fatalf("start: %+v", r)
	}
	return out.TaskID
}
func taskState(r Response) map[string]any {
	b, _ := json.Marshal(r.Result)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out
}

func TestCopyAndMoveStartFileStationTasks(t *testing.T) {
	e, _, ft, id := taskSetup(t, "files.copy", "files.move")
	taskID(t, request(e, "files.copy", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt", "dest_dir": "/home/dst"}))
	taskID(t, request(e, "files.move", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/src", "dest_dir": "/home/dst", "conflict": "overwrite"}))
	if len(ft.started) != 2 {
		t.Fatal(ft.started)
	}
	c, m := ft.started[0], ft.started[1]
	if c.runner != "alice" || c.kind != "copy" || c.params["path"] != "/home/a.txt" || c.params["dest_dir"] != "/home/dst" || c.params["overwrite"] != nil {
		t.Fatal(c)
	}
	if m.kind != "move" || m.params["overwrite"] != true {
		t.Fatal(m)
	}
}

func TestCopyAndMoveRefuseUnsafeRequestsBeforeStarting(t *testing.T) {
	e, _, ft, id := taskSetup(t, "files.copy", "files.move")
	for _, tc := range []struct{ op, path, dest, conflict, code string }{
		{"files.copy", "/home/a.txt", "/etc", "", "SCOPE_DENIED"},
		{"files.copy", "/etc/passwd", "/home/dst", "", "SCOPE_DENIED"},
		{"files.copy", "/home/missing", "/home/dst", "", "not-found"},
		{"files.copy", "/home/a.txt", "/home/missing", "", "not-found"},
		{"files.move", "/home", "/home/dst", "", "SCOPE_DENIED"},
		{"files.copy", "/home/src", "/home/src/inner", "", "INVALID_REQUEST"},
		{"files.move", "/home/src", "/home/src", "", "INVALID_REQUEST"},
		{"files.copy", "/home/a.txt", "/home/dst", "rename", "INVALID_REQUEST"},
		{"files.copy", "/home/a.txt", "/home/dst", "skip", "INVALID_REQUEST"},
	} {
		p := map[string]any{"delegation_id": id, "job_id": "job1", "path": tc.path, "dest_dir": tc.dest}
		if tc.conflict != "" {
			p["conflict"] = tc.conflict
		}
		if r := request(e, tc.op, p); r.Code != tc.code {
			t.Errorf("%+v: %+v", tc, r)
		}
	}
	if len(ft.started) != 0 {
		t.Fatal(ft.started)
	}
}

func TestDeleteRequiresExplicitRecursionAndARecycleBin(t *testing.T) {
	e, f, ft, id := taskSetup(t, "files.delete")
	f.apiReply = func(_ string, args map[string]any) (json.RawMessage, error) {
		isdir := args["path"] != "/home/a.txt"
		b, _ := json.Marshal(map[string]any{"files": []any{map[string]any{"path": args["path"], "isdir": isdir}}})
		return b, nil
	}
	del := func(path string, extra map[string]any) Response {
		p := map[string]any{"delegation_id": id, "job_id": "job1", "path": path}
		for k, v := range extra {
			p[k] = v
		}
		return request(e, "files.delete", p)
	}
	if r := del("/home/src", nil); r.Code != "recursive-required" {
		t.Fatal(r)
	}
	if r := del("/home", map[string]any{"recursive": true}); r.Code != "SCOPE_DENIED" {
		t.Fatal(r)
	}
	taskID(t, del("/home/src", map[string]any{"recursive": true}))
	taskID(t, del("/home/a.txt", nil))
	ft.recycle = false
	if r := del("/home/a.txt", nil); r.Code != "recycle-bin-disabled" {
		t.Fatal(r)
	}
	taskID(t, del("/home/a.txt", map[string]any{"permanent": true}))
	if len(ft.started) != 3 || ft.started[0].kind != "delete" || ft.started[0].params["recursive"] != true || ft.started[1].params["recursive"] != false {
		t.Fatal(ft.started)
	}
}

func TestTaskStatusFollowsTheTaskOfThisJobOnly(t *testing.T) {
	e, _, ft, id := taskSetup(t, "files.copy")
	task := taskID(t, request(e, "files.copy", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt", "dest_dir": "/home/dst"}))
	status := func(task string) Response {
		return request(e, "files.taskStatus", map[string]any{"delegation_id": id, "job_id": "job1", "task_id": task})
	}
	ft.progress = taskProgress{Progress: 0.5, Paths: []string{"/home/a.txt", "/home/dst"}}
	if out := taskState(status(task)); out["state"] != "running" || out["progress"] != 0.5 {
		t.Fatal(out)
	}
	ft.progress = taskProgress{Finished: true, Progress: 1}
	if out := taskState(status(task)); out["state"] != "finished" {
		t.Fatal(out)
	}
	if r := status(strings.Repeat("b", 64)); r.Code != "task-not-found" {
		t.Fatal(r)
	}
	other := create(t, e) // job1 again, but another grant
	if r := request(e, "files.taskStatus", map[string]any{"delegation_id": other, "job_id": "job1", "task_id": task}); r.Code != "task-not-found" {
		t.Fatal("task of another grant", r)
	}
}

func TestTaskStatusReportsFailureUnknownAndEscapes(t *testing.T) {
	e, _, ft, id := taskSetup(t, "files.copy")
	task := taskID(t, request(e, "files.copy", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt", "dest_dir": "/home/dst"}))
	status := func() Response {
		return request(e, "files.taskStatus", map[string]any{"delegation_id": id, "job_id": "job1", "task_id": task})
	}
	ft.progress, ft.err = taskProgress{}, &dsmError{Code: 599}
	if out := taskState(status()); out["state"] != "unknown" {
		t.Fatal(out)
	}
	ft.err = nil
	ft.progress = taskProgress{Code: 416} // an error before DSM marks the task finished
	if out := taskState(status()); out["state"] != "failed" || out["error"] != "NO_SPACE" {
		t.Fatal(out)
	}
	task = taskID(t, request(e, "files.copy", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt", "dest_dir": "/home/dst"}))
	ft.progress = taskProgress{Finished: true, Code: 414}
	if out := taskState(status()); out["state"] != "failed" || out["error"] != "destination-exists" {
		t.Fatal(out)
	}
	task = taskID(t, request(e, "files.copy", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt", "dest_dir": "/home/dst"}))
	ft.progress = taskProgress{Paths: []string{"/etc/passwd"}}
	if r := status(); r.Code != "SCOPE_DENIED" || len(ft.stopped) != 1 || ft.stopped[0] != "FileStation_task" {
		t.Fatal(r, ft.stopped)
	}
}

func TestTaskCancelStopsTheTask(t *testing.T) {
	e, f, ft, id := taskSetup(t, "files.delete")
	f.apiReply = func(_ string, args map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"files":[{"path":"/home/a.txt","isdir":false}]}`), nil
	}
	task := taskID(t, request(e, "files.delete", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt"}))
	r := request(e, "files.taskCancel", map[string]any{"delegation_id": id, "job_id": "job1", "task_id": task})
	if out := taskState(r); !r.OK || out["state"] != "canceled" || len(ft.stopped) != 1 {
		t.Fatal(r, ft.stopped)
	}
}

func TestRevokeStopsRunningTasks(t *testing.T) {
	e, _, ft, id := taskSetup(t, "files.copy")
	taskID(t, request(e, "files.copy", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt", "dest_dir": "/home/dst"}))
	if r := request(e, "delegate.revoke", map[string]any{"delegation_id": id}); !r.OK {
		t.Fatal(r)
	}
	if len(ft.stopped) != 1 {
		t.Fatal("revoke left the task running", ft.stopped)
	}
}

func TestTaskOperationsAreNotGrantedDirectly(t *testing.T) {
	e, _, _ := setup(t)
	for _, op := range []string{"files.taskStatus", "files.taskCancel"} {
		r := request(e, "delegate.create", map[string]any{"job_id": "job1", "ops": []string{op}, "scope": "home", "path": "/home", "ttl_seconds": 60})
		if r.Code != "INVALID_REQUEST" {
			t.Errorf("%s: %+v", op, r)
		}
	}
}

// DSM forgets a task once it has reported the end (599 afterwards), so the
// executor keeps the terminal answer and repeats it.
func TestTerminalTaskStatusRepeats(t *testing.T) {
	e, _, ft, id := taskSetup(t, "files.copy")
	start := func() string {
		return taskID(t, request(e, "files.copy", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt", "dest_dir": "/home/dst"}))
	}
	status := func(task string) map[string]any {
		return taskState(request(e, "files.taskStatus", map[string]any{"delegation_id": id, "job_id": "job1", "task_id": task}))
	}
	done, failed, canceled := start(), start(), start()
	ft.progress = taskProgress{Finished: true, Progress: 1}
	status(done)
	ft.progress = taskProgress{Finished: true, Code: 1003}
	status(failed)
	request(e, "files.taskCancel", map[string]any{"delegation_id": id, "job_id": "job1", "task_id": canceled})
	ft.progress, ft.err = taskProgress{}, &dsmError{Code: dsmNoSuchTask}
	for task, want := range map[string]string{done: "finished", failed: "failed", canceled: "canceled"} {
		for range 2 {
			if out := status(task); out["state"] != want {
				t.Fatal(want, out)
			}
		}
	}
	if out := status(failed); out["error"] != "destination-exists" {
		t.Fatal(out)
	}
	if r := request(e, "files.taskCancel", map[string]any{"delegation_id": id, "job_id": "job1", "task_id": done}); taskState(r)["state"] != "finished" || len(ft.stopped) != 1 {
		t.Fatal("cancel after the end", r, ft.stopped)
	}
}

// File Station reports -1 while it has not measured the task yet.
func TestTaskProgressIsAFraction(t *testing.T) {
	e, _, ft, id := taskSetup(t, "files.copy")
	task := taskID(t, request(e, "files.copy", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt", "dest_dir": "/home/dst"}))
	for dsm, want := range map[float64]float64{-1: 0, 0.4: 0.4, 3: 1} {
		ft.progress = taskProgress{Progress: dsm}
		if out := taskState(request(e, "files.taskStatus", map[string]any{"delegation_id": id, "job_id": "job1", "task_id": task})); out["progress"] != want {
			t.Errorf("%v: %v", dsm, out)
		}
	}
}

// DSM can end a delete it could not do without listing a cause; the path
// still being there is the failure.
func TestDeleteThatLeftThePathFails(t *testing.T) {
	e, f, ft, id := taskSetup(t, "files.delete")
	gone := false
	f.apiReply = func(_ string, args map[string]any) (json.RawMessage, error) {
		if gone {
			return nil, &dsmError{Code: 408}
		}
		return json.RawMessage(`{"files":[{"path":"/home/a.txt","isdir":false}]}`), nil
	}
	start := func() string {
		return taskID(t, request(e, "files.delete", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt"}))
	}
	status := func(task string) map[string]any {
		return taskState(request(e, "files.taskStatus", map[string]any{"delegation_id": id, "job_id": "job1", "task_id": task}))
	}
	ft.progress = taskProgress{Finished: true, Progress: 1}
	denied, kept, deleted := start(), start(), start()
	f.permissionErr = &dsmError{Code: 407}
	if out := status(denied); out["state"] != "failed" || out["error"] != "PERMISSION_DENIED" {
		t.Fatal(out)
	}
	f.permissionErr = nil
	if out := status(kept); out["state"] != "failed" || out["error"] != "DSM_FAILED" {
		t.Fatal(out)
	}
	gone = true
	if out := status(deleted); out["state"] != "finished" {
		t.Fatal(out)
	}
}

// DSM answers a delete the ACL refused with only the generic errors[{code:900}]
// ; the denial is the path still there and its folder refusing a
// write, and a 900 without that refusal stays DSM_FAILED.
func TestRefusedDeleteWithGenericFailureIsPermissionDenied(t *testing.T) {
	e, f, ft, id := taskSetup(t, "files.delete")
	gone := false
	f.apiReply = func(_ string, args map[string]any) (json.RawMessage, error) {
		if gone {
			return nil, &dsmError{Code: 408}
		}
		return json.RawMessage(`{"files":[{"path":"/home/a.txt","isdir":false}]}`), nil
	}
	start := func() string {
		return taskID(t, request(e, "files.delete", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt"}))
	}
	status := func(task string) map[string]any {
		return taskState(request(e, "files.taskStatus", map[string]any{"delegation_id": id, "job_id": "job1", "task_id": task}))
	}
	ft.progress = taskProgress{Finished: true, Code: dsmDeleteFailed}
	denied, kept, deleted := start(), start(), start()
	f.permissionErr = &dsmError{Code: 407}
	if out := status(denied); out["state"] != "failed" || out["error"] != "PERMISSION_DENIED" {
		t.Fatal(out)
	}
	f.permissionErr = nil
	if out := status(kept); out["state"] != "failed" || out["error"] != "DSM_FAILED" {
		t.Fatal(out)
	}
	gone = true
	if out := status(deleted); out["state"] != "failed" || out["error"] != "DSM_FAILED" {
		t.Fatal(out)
	}
}

func TestGenericFailureOfACopyStaysDSMFailed(t *testing.T) {
	e, f, ft, id := taskSetup(t, "files.copy")
	f.permissionErr = &dsmError{Code: 407}
	task := taskID(t, request(e, "files.copy", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home/a.txt", "dest_dir": "/home/dst"}))
	ft.progress = taskProgress{Finished: true, Code: dsmDeleteFailed}
	if out := taskState(request(e, "files.taskStatus", map[string]any{"delegation_id": id, "job_id": "job1", "task_id": task})); out["state"] != "failed" || out["error"] != "DSM_FAILED" {
		t.Fatal(out)
	}
}
