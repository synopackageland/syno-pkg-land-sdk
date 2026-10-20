package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/taskscript"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type schedulerFixture struct {
	changed, foreignOwner, duplicate bool
	script                           string
	// pkg names the tasks; empty is TestApp.
	pkg string
	// deleteReply replaces the delete envelope, as DSM sends it.
	deleteReply string
	deleted     []string
}

func (s *schedulerFixture) Exec(_ context.Context, _ string, args []string) ([]byte, error) {
	api, method := "", ""
	for _, a := range args {
		if strings.HasPrefix(a, "api=") {
			api = strings.TrimPrefix(a, "api=")
		}
		if strings.HasPrefix(a, "method=") {
			method = strings.TrimPrefix(a, "method=")
		}
	}
	owner := "root"
	if s.foreignOwner {
		owner = "other"
	}
	pkg := s.pkg
	if pkg == "" {
		pkg = "TestApp"
	}
	generated, _ := taskscript.Generate(pkg, strings.Repeat("b", 64))
	script := generated[1].Script
	if api == "SYNO.Core.EventScheduler" {
		script = generated[0].Script
	}
	if s.script != "" {
		script = s.script
	}
	if s.changed {
		script = "unrelated"
	}
	var data any
	if method == "delete" {
		s.deleted = append(s.deleted, api)
		if api == "SYNO.Core.TaskScheduler" && !contains(args, `tasks=[{"id":7,"real_owner":"root"}]`) {
			return nil, errors.New("wrong delete reference")
		}
		if s.deleteReply != "" {
			return []byte(s.deleteReply), nil
		}
		data = map[string]any{}
	} else if api == "SYNO.Core.EventScheduler" {
		data = map[string]any{"task_name": pkg + " executor boot", "owner": map[string]string{"0": owner}, "operation": script, "operation_type": "script", "event": "bootup", "enable": true}
	} else if method == "list" {
		tasks := []map[string]any{{"name": pkg + " executor control", "id": 7}, {"name": pkg + " executor boot", "id": 1000}}
		if s.duplicate {
			tasks = append(tasks, tasks[0])
		}
		data = map[string]any{"tasks": tasks, "total": len(tasks)}
	} else {
		data = map[string]any{"name": pkg + " executor control", "id": 7, "owner": owner, "real_owner": owner, "enable": false, "type": "script", "extra": map[string]any{"script": script}}
	}
	return json.Marshal(map[string]any{"success": true, "data": data})
}
func TestPurgeDeletesOnlyExactOwnRootTasksAndProtectedState(t *testing.T) {
	for _, mode := range []string{"exact", "script changed", "owner changed", "duplicate control", "foreign name"} {
		t.Run(mode, func(t *testing.T) {
			cmd := &schedulerFixture{changed: mode == "script changed", foreignOwner: mode == "owner changed", duplicate: mode == "duplicate control"}
			inventory := taskInventory{Package: "TestApp", PolicyDigest: strings.Repeat("b", 64), Tasks: []approvedTask{{Kind: "boot", Name: "TestApp executor boot", ScriptDigest: canonicalTaskDigest(t, "boot")}, {Kind: "control", Name: "TestApp executor control", ScriptDigest: canonicalTaskDigest(t, "control")}}}
			if mode == "foreign name" {
				inventory.Tasks[0].Name = "OtherApp executor boot"
			}
			state := filepath.Join(t.TempDir(), "state")
			socket := filepath.Join(t.TempDir(), "socket")
			os.MkdirAll(filepath.Join(state, "runtime"), 0700)
			os.MkdirAll(socket, 0700)
			if err := purgeWithInventory(context.Background(), &liveDSM{Command: cmd}, "TestApp", inventory, state, filepath.Join(state, "runtime"), socket, func(string) {}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(state); !os.IsNotExist(err) {
				t.Fatal("state survived")
			}
			want := 0
			if mode == "exact" {
				want = 2
			}
			if mode == "duplicate control" {
				want = 1
			}
			if len(cmd.deleted) != want {
				t.Fatal("deleted mismatched tasks", cmd.deleted)
			}
		})
	}
}

func TestSnapshotRequiresBothExactSchedulerDefinitionsBeforeWritingInventory(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact setup", true: "changed root task"}[mismatch], func(t *testing.T) {
			dir := realTestPath(t, t.TempDir())
			ed := strings.Repeat("a", 64)
			pd := strings.Repeat("b", 64)
			tasks := []map[string]string{}
			generated, err := taskscript.Generate("TestApp", pd)
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range generated {
				tasks = append(tasks, map[string]string{"kind": task.Kind, "name": task.Name, "header": task.Header, "script": task.Script})
			}
			setup := map[string]any{"package": "TestApp", "executorDigest": ed, "policyDigest": pd, "implementation": "executor", "tasks": tasks}
			b, _ := json.Marshal(setup)
			path := filepath.Join(dir, "setup.json")
			os.WriteFile(path, b, 0600)
			command := &snapshotFixture{mismatch: mismatch, tasks: tasks}
			err = snapshotTasks(context.Background(), &liveDSM{Command: command}, "TestApp", ed, pd, path, dir)
			if (err != nil) != mismatch {
				t.Fatal("snapshot validation", err)
			}
			b, readErr := os.ReadFile(filepath.Join(dir, "tasks.json"))
			if mismatch {
				if !os.IsNotExist(readErr) {
					t.Fatal("unverified inventory persisted")
				}
				return
			}
			var inv taskInventory
			if json.Unmarshal(b, &inv) != nil || !validInventory("TestApp", inv) {
				t.Fatal("missing verified inventory")
			}
		})
	}
}

type snapshotFixture struct {
	mismatch bool
	pkg      string
	tasks    []map[string]string
}

func (s *snapshotFixture) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	f := &schedulerFixture{changed: s.mismatch, pkg: s.pkg}
	for _, a := range args {
		if a == "api=SYNO.Core.EventScheduler" {
			f.script = s.tasks[0]["script"]
		}
		if a == "api=SYNO.Core.TaskScheduler" {
			f.script = s.tasks[1]["script"]
		}
	}
	return f.Exec(ctx, path, args)
}

func canonicalTaskDigest(t *testing.T, kind string) string {
	t.Helper()
	tasks, err := taskscript.Generate("TestApp", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Kind == kind {
			return hashBytes([]byte(task.Script))
		}
	}
	t.Fatal("task kind")
	return ""
}

// preuninst took 15-23 s; time each scheduler phase and the file
// removal separately, naming the package, so a device log shows which waits.
func TestPurgeLogsPhaseTimingWithPackageName(t *testing.T) {
	cmd := &schedulerFixture{}
	inventory := taskInventory{Package: "TestApp", PolicyDigest: strings.Repeat("b", 64), Tasks: []approvedTask{{Kind: "boot", Name: "TestApp executor boot", ScriptDigest: canonicalTaskDigest(t, "boot")}, {Kind: "control", Name: "TestApp executor control", ScriptDigest: canonicalTaskDigest(t, "control")}}}
	state := filepath.Join(t.TempDir(), "state")
	socket := filepath.Join(t.TempDir(), "socket")
	os.MkdirAll(filepath.Join(state, "runtime"), 0700)
	os.MkdirAll(socket, 0700)
	logs := []string{}
	if err := purgeWithInventory(context.Background(), &liveDSM{Command: cmd}, "TestApp", inventory, state, filepath.Join(state, "runtime"), socket, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatal(err)
	}
	task := regexp.MustCompile(`^task cleanup timing: package=TestApp kind=(boot|control) lookup_ms=\d+ delete_ms=\d+ verify_ms=\d+$`)
	total := regexp.MustCompile(`^purge timing: package=TestApp tasks_ms=\d+ files_ms=\d+$`)
	kinds := []string{}
	for _, line := range logs {
		if m := task.FindStringSubmatch(line); m != nil {
			kinds = append(kinds, m[1])
		}
	}
	if strings.Join(kinds, ",") != "boot,control" || len(logs) == 0 || !total.MatchString(logs[len(logs)-1]) {
		t.Fatal(logs)
	}
}

// EventScheduler delete v1 and TaskScheduler delete v2 answer a success
// without data; that is a confirmed delete, while a refusal stays unconfirmed.
func TestPurgeConfirmsDatalessDeleteSuccess(t *testing.T) {
	for reply, unconfirmed := range map[string]bool{
		`{"success":true}`:                        false,
		`{"httpd_restart":false,"success":true}`:  false,
		`{"success":false,"error":{"code":4800}}`: true,
		`{"success":true,"error":{"code":4800}}`:  true,
	} {
		cmd := &schedulerFixture{deleteReply: reply}
		inventory := taskInventory{Package: "TestApp", PolicyDigest: strings.Repeat("b", 64), Tasks: []approvedTask{{Kind: "boot", Name: "TestApp executor boot", ScriptDigest: canonicalTaskDigest(t, "boot")}, {Kind: "control", Name: "TestApp executor control", ScriptDigest: canonicalTaskDigest(t, "control")}}}
		logs := []string{}
		purgeTasks(context.Background(), &liveDSM{Command: cmd}, "TestApp", inventory, func(s string) { logs = append(logs, s) })
		found := 0
		for _, line := range logs {
			if strings.HasPrefix(line, "task cleanup unconfirmed: ") {
				found++
			}
		}
		if (found == 2) != unconfirmed || (found != 0 && found != 2) || len(cmd.deleted) != 2 {
			t.Fatal(reply, logs)
		}
	}
}

// overlapCommand answers like next, but holds the boot task's readback until
// a control task call arrives, or gives up after a while.
type overlapCommand struct {
	next    Command
	control chan struct{}
	once    sync.Once
	overlap atomic.Bool
}

func (o *overlapCommand) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	if contains(args, "api=SYNO.Core.TaskScheduler") {
		o.once.Do(func() { close(o.control) })
	} else {
		select {
		case <-o.control:
			o.overlap.Store(true)
		case <-time.After(time.Second):
		}
	}
	return o.next.Exec(ctx, path, args)
}

// The boot and control tasks are read back from DSM at the same time: on a
// measured NAS each scheduler call took 0.2-2 s.
func TestTheTwoApprovalTasksAreReadBackAtOnce(t *testing.T) {
	pd := strings.Repeat("b", 64)
	generated, err := taskscript.Generate("TestApp", pd)
	if err != nil {
		t.Fatal(err)
	}
	tasks := []map[string]string{}
	for _, task := range generated {
		tasks = append(tasks, map[string]string{"kind": task.Kind, "name": task.Name, "header": task.Header, "script": task.Script})
	}
	inv, err := canonicalInventory("TestApp", pd)
	if err != nil {
		t.Fatal(err)
	}
	command := &overlapCommand{next: &snapshotFixture{tasks: tasks}, control: make(chan struct{})}
	if err := liveTasks(context.Background(), &liveDSM{Command: command}, "TestApp", pd, inv); err != nil {
		t.Fatal(err)
	}
	if !command.overlap.Load() {
		t.Fatal("the boot task was read back before the control task started")
	}
}

// countingCommand counts TaskScheduler lists; gone makes get answer 4801, as
// DSM does for an id that no longer exists.
type countingCommand struct {
	next  Command
	lists atomic.Int32
	gone  atomic.Bool
}

func (c *countingCommand) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	if contains(args, "api=SYNO.Core.TaskScheduler") && contains(args, "method=list") {
		c.lists.Add(1)
	}
	if c.gone.Load() && contains(args, "api=SYNO.Core.TaskScheduler") && contains(args, "method=get") {
		return []byte(`{"success":false,"error":{"code":4801}}`), nil
	}
	return c.next.Exec(ctx, path, args)
}

// A root step's approval check right after the startup snapshot gets the
// control task by the id that snapshot listed; an id that is gone is listed again.
func TestApprovalCheckGetsTheControlTaskTheSnapshotListed(t *testing.T) {
	dir := realTestPath(t, t.TempDir())
	ed, pd := strings.Repeat("a", 64), strings.Repeat("b", 64)
	generated, err := taskscript.Generate("TestApp", pd)
	if err != nil {
		t.Fatal(err)
	}
	tasks := []map[string]string{}
	for _, task := range generated {
		tasks = append(tasks, map[string]string{"kind": task.Kind, "name": task.Name, "header": task.Header, "script": task.Script})
	}
	b, _ := json.Marshal(map[string]any{"package": "TestApp", "executorDigest": ed, "policyDigest": pd, "implementation": "executor", "tasks": tasks})
	path := filepath.Join(dir, "setup.json")
	os.WriteFile(path, b, 0600)
	command := &countingCommand{next: &snapshotFixture{tasks: tasks}}
	d := &liveDSM{Command: command}
	if err = snapshotTasks(context.Background(), d, "TestApp", ed, pd, path, dir); err != nil {
		t.Fatal(err)
	}
	inv, err := canonicalInventory("TestApp", pd)
	if err != nil {
		t.Fatal(err)
	}
	if err = liveTasks(context.Background(), d, "TestApp", pd, inv); err != nil {
		t.Fatal(err)
	}
	if n := command.lists.Load(); n != 1 {
		t.Fatalf("TaskScheduler lists after snapshot and approval check: %d, want 1", n)
	}
	command.gone.Store(true)
	if err = liveTasks(context.Background(), d, "TestApp", pd, inv); err == nil {
		t.Fatal("a control task whose id is gone passed the approval check")
	}
	if n := command.lists.Load(); n != 2 {
		t.Fatalf("a gone id was not listed again: %d lists", n)
	}
}

// Cleanup decides what to delete from a fresh list, never a cached id.
func TestPurgeListsTheControlTaskAfterASnapshot(t *testing.T) {
	command := &countingCommand{next: &schedulerFixture{}}
	d := &liveDSM{Command: command}
	inv, err := canonicalInventory("TestApp", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err = liveTasks(context.Background(), d, "TestApp", inv.PolicyDigest, inv); err != nil {
		t.Fatal(err)
	}
	before := command.lists.Load()
	purgeTasks(context.Background(), d, "TestApp", inv, func(string) {})
	if command.lists.Load() == before {
		t.Fatal("purge reused a cached control task id")
	}
}
