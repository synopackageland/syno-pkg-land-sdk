package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/taskscript"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

type approvedTask struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	ScriptDigest string `json:"script_digest"`
}
type taskInventory struct {
	Package      string         `json:"package"`
	PolicyDigest string         `json:"policy_digest"`
	Tasks        []approvedTask `json:"tasks"`
}

func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func validInventory(pkg string, inventory taskInventory) bool {
	if inventory.Package != pkg || len(inventory.Tasks) != 2 || !idRE.MatchString(inventory.PolicyDigest) {
		return false
	}
	seen := map[string]bool{}
	for _, t := range inventory.Tasks {
		if !contains([]string{"boot", "control"}, t.Kind) || seen[t.Kind] || t.Name != pkg+" executor "+t.Kind || !idRE.MatchString(t.ScriptDigest) {
			return false
		}
		seen[t.Kind] = true
	}
	return true
}
func rootOwner(raw json.RawMessage, boot bool) bool {
	var owner string
	if json.Unmarshal(raw, &owner) == nil {
		return owner == "root"
	}
	var owners map[string]string
	return boot && json.Unmarshal(raw, &owners) == nil && len(owners) == 1 && owners["0"] == "root"
}

// listedControlID is the id of the one Task Scheduler task named name, which
// it remembers for cached readbacks; a missing or repeated name is refused.
func listedControlID(ctx context.Context, d *liveDSM, name string) (int, error) {
	d.controlIDs.Delete(name)
	b, err := listControlTasks(ctx, d)
	if err != nil {
		return 0, err
	}
	var list struct {
		Tasks []scheduledTask `json:"tasks"`
		Total *int            `json:"total"`
	}
	if json.Unmarshal(b, &list) != nil || list.Total == nil || *list.Total != len(list.Tasks) || len(list.Tasks) > 500 {
		return 0, errors.New("incomplete scheduler list")
	}
	count := 0
	var id int
	for _, t := range list.Tasks {
		if t.Name == name {
			count++
			n, e := strconv.Atoi(string(t.ID))
			if e != nil || n < 0 {
				return 0, errors.New("task id")
			}
			id = n
		}
	}
	if count != 1 {
		return 0, errors.New("missing or duplicate task")
	}
	d.controlIDs.Store(name, id)
	return id, nil
}

// taskReference reads one approval task back and checks it exactly. The boot
// task is got by name: EventScheduler keys tasks by name, and DSM refuses a
// second one (117 DUPLICATE_PRIMARY_KEY). Task Scheduler names repeat, so
// the control task is listed for its id; cached gets the id an earlier list
// showed instead, and lists again when that id is gone or names another task.
func taskReference(ctx context.Context, d *liveDSM, task approvedTask, expected string, cached bool) (string, string, map[string]any, error) {
	api, version := "SYNO.Core.EventScheduler", "1"
	ref := map[string]any{"task_name": task.Name}
	boot := task.Kind == "boot"
	if !boot {
		api = "SYNO.Core.TaskScheduler"
		id, ok := 0, false
		if cached {
			var v any
			v, ok = d.controlIDs.Load(task.Name)
			id, _ = v.(int)
		}
		if !ok {
			var err error
			if id, err = listedControlID(ctx, d, task.Name); err != nil {
				return "", "", nil, err
			}
			cached = false
		}
		ref = map[string]any{"id": id, "real_owner": "root"}
	}
	var b json.RawMessage
	var err error
	if boot {
		b, err = d.call(ctx, api, "get", version, "", ref)
	} else {
		b, err = onUnsupportedVersion(func() (json.RawMessage, error) { return d.call(ctx, api, "get", "4", "", ref) },
			func() (json.RawMessage, error) { return d.call(ctx, api, "get", "3", "", ref) })
	}
	if cached && !boot && !sameControlTask(b, err, task.Name, ref["id"].(int)) {
		return taskReference(ctx, d, task, expected, false)
	}
	if err != nil {
		return "", "", nil, err
	}
	var got struct {
		Name          string          `json:"name"`
		TaskName      string          `json:"task_name"`
		ID            json.Number     `json:"id"`
		Owner         json.RawMessage `json:"owner"`
		RealOwner     string          `json:"real_owner"`
		Enable        *bool           `json:"enable"`
		Type          string          `json:"type"`
		Event         string          `json:"event"`
		OperationType string          `json:"operation_type"`
		Operation     string          `json:"operation"`
		Extra         struct {
			Script string `json:"script"`
		} `json:"extra"`
	}
	if json.Unmarshal(b, &got) != nil || !rootOwner(got.Owner, boot) || got.Enable == nil {
		return "", "", nil, errors.New("task owner")
	}
	script := got.Operation
	if boot {
		if got.TaskName != task.Name || !*got.Enable || got.Event != "bootup" || got.OperationType != "script" {
			return "", "", nil, errors.New("boot definition")
		}
	} else {
		if got.Name != task.Name || string(got.ID) != strconv.Itoa(ref["id"].(int)) || got.RealOwner != "root" || *got.Enable || got.Type != "script" {
			return "", "", nil, errors.New("control definition")
		}
		script = got.Extra.Script
	}
	if script != expected || hashBytes([]byte(script)) != task.ScriptDigest {
		return "", "", nil, errors.New("task script mismatch")
	}
	if !boot {
		version = "2"
		ref = map[string]any{"tasks": []map[string]any{ref}}
	}
	return api, version, ref, nil
}

// sameControlTask reports whether a get of a cached id answered that id under
// name; DSM answers 4801 for an id that no longer exists.
func sameControlTask(b json.RawMessage, err error, name string, id int) bool {
	var refused *dsmError
	if errors.As(err, &refused) && refused.Code == 4801 {
		return false
	}
	var got struct {
		Name string      `json:"name"`
		ID   json.Number `json:"id"`
	}
	return err != nil || (json.Unmarshal(b, &got) == nil && got.Name == name && string(got.ID) == strconv.Itoa(id))
}

// readBackTasks checks each task against its script in the scheduler, all at
// once: each scheduler call can take seconds. It answers the first refusal.
// cached gets the control task by the id the last list showed.
func readBackTasks(ctx context.Context, d *liveDSM, tasks []approvedTask, scripts []string, cached bool) error {
	errs := make([]error, len(tasks))
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, errs[i] = taskReference(ctx, d, task, scripts[i], cached)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// Setup records are copied into root state only after exact scheduler readback.
// Package-controlled names can never expand deletion beyond this package's pair.
func snapshotTasks(ctx context.Context, d *liveDSM, pkg, ed, pd, setupPath, runtime string) error {
	b, _, err := readLifecycleFile(setupPath, 16384)
	if err != nil {
		return err
	}
	var setup struct {
		Package        string          `json:"package"`
		ExecutorDigest string          `json:"executorDigest"`
		PolicyDigest   string          `json:"policyDigest"`
		Policy         json.RawMessage `json:"policy"`
		Implementation string          `json:"implementation"`
		// SDK is the version the CLI built the executor from (CLI 0.9.0 and later).
		SDK   string `json:"sdk"`
		Tasks []struct {
			Kind   string `json:"kind"`
			Name   string `json:"name"`
			Header string `json:"header"`
			Script string `json:"script"`
		} `json:"tasks"`
	}
	if strictJSON(b, &setup) != nil || setup.Package != pkg || setup.ExecutorDigest != ed || setup.PolicyDigest != pd || setup.Implementation != "executor" {
		return errors.New("setup pins")
	}
	generated, err := taskscript.Generate(pkg, pd)
	if err != nil || len(setup.Tasks) != len(generated) {
		return errors.New("setup task inventory")
	}
	for i, task := range generated {
		hint := setup.Tasks[i]
		if hint.Kind != task.Kind || hint.Name != task.Name || hint.Header != task.Header || hint.Script != task.Script {
			return errors.New("setup task mismatch")
		}
	}
	inv, err := canonicalInventory(pkg, pd)
	if err != nil {
		return err
	}
	scripts := make([]string, len(generated))
	for i, task := range generated {
		scripts[i] = task.Script
	}
	if err = readBackTasks(ctx, d, inv.Tasks, scripts, false); err != nil {
		return err
	}
	b, err = json.Marshal(inv)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(runtime, ".tasks-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(runtime, "tasks.json"))
}

// canonicalInventory is the boot and control tasks the CLI creates for this
// package and policy. Cleanup deletes a task only when its script is exactly
// the canonical one, so the inventory needs no other evidence.
func canonicalInventory(pkg, pd string) (taskInventory, error) {
	inv := taskInventory{Package: pkg, PolicyDigest: pd}
	generated, err := taskscript.Generate(pkg, pd)
	if err != nil {
		return inv, err
	}
	for _, task := range generated {
		inv.Tasks = append(inv.Tasks, approvedTask{Kind: task.Kind, Name: task.Name, ScriptDigest: hashBytes([]byte(task.Script))})
	}
	if !validInventory(pkg, inv) {
		return inv, errors.New("task inventory")
	}
	return inv, nil
}

// pinnedInventory rebuilds the inventory of an install that has no tasks.json
// (CLI 0.9.0 wrote a setup.json the snapshot refused) from the approved policy.
func pinnedInventory(ctx context.Context, pkg, runtime string) (taskInventory, error) {
	if err := trustedTree(runtime); err != nil {
		return taskInventory{}, err
	}
	pd, err := digestFile(ctx, filepath.Join(runtime, "policy.json"), maxPolicyBytes)
	if err != nil {
		return taskInventory{}, err
	}
	return canonicalInventory(pkg, pd)
}

// selectionTask is the script of the install-selected scope task the DSM
// window creates as "<pkg> executor selection"; only scope, actor and
// generation vary, and it runs this package's own executor.
func selectionTask(pkg string) *regexp.Regexp {
	quoted := `'(?:[^']|'\\'')*'`
	return regexp.MustCompile(`^#!/bin/sh\n# synopkgland executor selection\nset -eu\nexport PATH=/usr/bin:/bin\nd=/usr/local/etc/synopkgland/` + pkg + `/runtime\n` +
		`\{ echo [a-f0-9]{64}"  \$d/executor"; echo [a-f0-9]{64}"  \$d/policy\.json"; \}\|sha256sum -c - >/dev/null\n` +
		`exec "\$d/executor" select-scope --package '` + pkg + `' --scope ` + quoted + ` --actor ` + quoted + ` --expected-generation [0-9]+\n$`)
}

// purgeSelectionTasks deletes this package's "<pkg> executor selection"
// tasks; the CLI leaves them after the scope is confirmed.
func purgeSelectionTasks(ctx context.Context, d *liveDSM, pkg string, log func(string)) {
	name := pkg + " executor selection"
	tasks, err := scheduledTasks(ctx, d)
	if err != nil {
		log("task cleanup skipped: selection list unavailable")
		return
	}
	shape := selectionTask(pkg)
	for _, t := range tasks {
		id, err := strconv.Atoi(string(t.ID))
		if t.Name != name || err != nil || id < 0 {
			continue
		}
		ref := map[string]any{"id": id, "real_owner": "root"}
		got, err := onUnsupportedVersion(func() (json.RawMessage, error) { return d.call(ctx, "SYNO.Core.TaskScheduler", "get", "4", "", ref) },
			func() (json.RawMessage, error) { return d.call(ctx, "SYNO.Core.TaskScheduler", "get", "3", "", ref) })
		var def struct {
			Name      string          `json:"name"`
			Owner     json.RawMessage `json:"owner"`
			RealOwner string          `json:"real_owner"`
			Type      string          `json:"type"`
			Extra     struct {
				Script string `json:"script"`
			} `json:"extra"`
		}
		if err != nil || json.Unmarshal(got, &def) != nil || def.Name != name || !rootOwner(def.Owner, false) || def.RealOwner != "root" || def.Type != "script" || !shape.MatchString(def.Extra.Script) {
			log("task cleanup skipped: selection definition mismatch or unavailable")
			continue
		}
		// A failed delete call can still have deleted: the task list decides.
		reason := ""
		if err = d.mutate(ctx, "SYNO.Core.TaskScheduler", "delete", "2", map[string]any{"tasks": []map[string]any{ref}}); err != nil {
			reason = " delete_error=" + failureReason(err)
		}
		remains, err := selectionRemains(ctx, d, id)
		switch {
		case err != nil:
			log(fmt.Sprintf("task cleanup unconfirmed: selection id=%d%s", id, reason))
		case remains:
			log(fmt.Sprintf("task cleanup failed: selection id=%d remains%s", id, reason))
		default:
			log(fmt.Sprintf("task cleanup: selection id=%d%s", id, reason))
		}
	}
}

// selectionRemains reports whether the scheduler still lists task id; several
// selection tasks share one name, so the id identifies it.
func selectionRemains(ctx context.Context, d *liveDSM, id int) (bool, error) {
	tasks, err := scheduledTasks(ctx, d)
	if err != nil {
		return false, err
	}
	for _, t := range tasks {
		if string(t.ID) == strconv.Itoa(id) {
			return true, nil
		}
	}
	return false, nil
}

func readTaskInventory(runtime string) (taskInventory, error) {
	var inv taskInventory
	f, err := openRegular(filepath.Join(runtime, "tasks.json"))
	if err != nil {
		return inv, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !ownedByRoot(info) || info.Mode().Perm() != 0600 {
		return inv, errors.New("untrusted task inventory")
	}
	b, err := readLimited(f, 16384)
	if err != nil {
		return inv, err
	}
	err = strictJSON(b, &inv)
	return inv, err
}
func purgeTasks(ctx context.Context, d *liveDSM, pkg string, inv taskInventory, log func(string)) {
	if !validInventory(pkg, inv) {
		log("task cleanup skipped: invalid protected inventory")
		return
	}
	generated, err := taskscript.Generate(pkg, inv.PolicyDigest)
	if err != nil {
		log("task cleanup skipped: invalid protected pins")
		return
	}
	for _, task := range inv.Tasks {
		expected := ""
		for _, canonical := range generated {
			if canonical.Kind == task.Kind {
				expected = canonical.Script
			}
		}
		started := time.Now()
		api, version, ref, err := taskReference(ctx, d, task, expected, false)
		if err != nil {
			log("task cleanup skipped: " + task.Kind + " definition mismatch or unavailable")
			continue
		}
		looked := time.Now()
		if err = d.mutate(ctx, api, "delete", version, ref); err != nil {
			log("task cleanup unconfirmed: " + task.Kind)
		}
		deleted := time.Now()
		// The scheduler has no conditional delete. Accept the admin edit window,
		// delete exactly once, and observe the name afterward without another delete.
		if taskRemains(ctx, d, task) {
			log("task cleanup: " + task.Kind + " same-named task remains after delete")
		}
		log(fmt.Sprintf("task cleanup timing: package=%s kind=%s lookup_ms=%d delete_ms=%d verify_ms=%d", pkg, task.Kind,
			looked.Sub(started).Milliseconds(), deleted.Sub(looked).Milliseconds(), time.Since(deleted).Milliseconds()))
	}
}

func purgeWithInventory(ctx context.Context, d *liveDSM, pkg string, inv taskInventory, state, runtime, socket string, log func(string)) error {
	if runtime != filepath.Join(state, "runtime") {
		return errors.New("runtime outside package protected state")
	}
	started := time.Now()
	purgeTasks(ctx, d, pkg, inv, log)
	purgeSelectionTasks(ctx, d, pkg, log)
	cleaned := time.Now()
	err := finishControl(state, runtime, socket, true)
	log(fmt.Sprintf("purge timing: package=%s tasks_ms=%d files_ms=%d", pkg, cleaned.Sub(started).Milliseconds(), time.Since(cleaned).Milliseconds()))
	return err
}

func taskRemains(ctx context.Context, d *liveDSM, task approvedTask) bool {
	if task.Kind == "boot" {
		b, err := d.call(ctx, "SYNO.Core.EventScheduler", "get", "1", "", map[string]any{"task_name": task.Name})
		if err != nil {
			return false
		}
		var got struct {
			Name string `json:"task_name"`
		}
		return json.Unmarshal(b, &got) == nil && got.Name == task.Name
	}
	tasks, err := scheduledTasks(ctx, d)
	if err != nil {
		return false
	}
	for _, t := range tasks {
		if t.Name == task.Name {
			return true
		}
	}
	return false
}

// DSM 7.1 has TaskScheduler list v2 (offset) and get v3 only. A read falls back
// solely when DSM reports the method version unsupported; any other refusal stands.
func onUnsupportedVersion(current, legacy func() (json.RawMessage, error)) (json.RawMessage, error) {
	b, err := current()
	var refused *dsmError
	if errors.As(err, &refused) && versionUnsupported(refused.Code) {
		return legacy()
	}
	return b, err
}

// DSM 7.1.1 returns 104 for a version above maxVersion (get v4) and 103 for a
// method absent at an existing version (list v3).
func versionUnsupported(code int) bool { return code == 103 || code == 104 }

type scheduledTask struct {
	Name string      `json:"name"`
	ID   json.Number `json:"id"`
}

// scheduledTasks is the name and id of every Task Scheduler task.
func scheduledTasks(ctx context.Context, d *liveDSM) ([]scheduledTask, error) {
	b, err := listControlTasks(ctx, d)
	if err != nil {
		return nil, err
	}
	var list struct {
		Tasks []scheduledTask `json:"tasks"`
	}
	err = json.Unmarshal(b, &list)
	return list.Tasks, err
}

func listControlTasks(ctx context.Context, d *liveDSM) (json.RawMessage, error) {
	return onUnsupportedVersion(func() (json.RawMessage, error) {
		return d.call(ctx, "SYNO.Core.TaskScheduler", "list", "3", "", map[string]any{"start": 0, "limit": 500})
	}, func() (json.RawMessage, error) {
		return d.call(ctx, "SYNO.Core.TaskScheduler", "list", "2", "", map[string]any{"offset": 0, "limit": 500})
	})
}
