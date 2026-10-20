package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The setup.json the CLI writes (executorPlan, with "sdk"): 0.9.0 generated
// from its dist/runtime/executor-build.js, 0.9.1 copied from the CLI's
// test/fixtures/executor-setup. Strict parsing refused "sdk", so no tasks.json
// was written and uninstall left both root tasks. Current fixtures follow the
// canonical SDK scripts; the historical 0.9.0 script now requires reapproval.
func TestSnapshotAcceptsCurrentCLISetupAndRejectsOldTaskDefinitions(t *testing.T) {
	fixtures := []string{"testdata/cli-0.9.0-setup.json"}
	for _, name := range []string{"install-selected", "delegate", "jobs", "root-ops"} {
		fixtures = append(fixtures, "testdata/cli-setup/"+name+".json")
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			b, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			var plan struct {
				Package        string              `json:"package"`
				ExecutorDigest string              `json:"executorDigest"`
				PolicyDigest   string              `json:"policyDigest"`
				Tasks          []map[string]string `json:"tasks"`
				SDK            string              `json:"sdk"`
			}
			if json.Unmarshal(b, &plan) != nil || plan.SDK == "" || len(plan.Tasks) != 2 {
				t.Fatal("fixture", plan)
			}
			dir := realTestPath(t, t.TempDir())
			path := filepath.Join(dir, "setup.json")
			os.WriteFile(path, b, 0600)
			err = snapshotTasks(context.Background(), &liveDSM{Command: &snapshotFixture{pkg: plan.Package, tasks: plan.Tasks}}, plan.Package, plan.ExecutorDigest, plan.PolicyDigest, path, dir)
			if fixture == "testdata/cli-0.9.0-setup.json" {
				if err == nil || !strings.Contains(err.Error(), "setup task mismatch") {
					t.Fatal("old task definition accepted", err)
				}
				if _, err = os.Stat(filepath.Join(dir, "tasks.json")); !os.IsNotExist(err) {
					t.Fatal("old task inventory persisted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(dir, "tasks.json")); err != nil {
				t.Fatal("no inventory", err)
			}
		})
	}
}

// An install without tasks.json (CLI 0.9.0) is cleaned from the pinned policy:
// the canonical tasks are exact, so only this package's own tasks match.
func TestCanonicalInventoryIsTheSnapshotInventory(t *testing.T) {
	inv, err := canonicalInventory("TestApp", strings.Repeat("b", 64))
	if err != nil || !validInventory("TestApp", inv) || inv.Tasks[0].ScriptDigest != canonicalTaskDigest(t, "boot") || inv.Tasks[1].ScriptDigest != canonicalTaskDigest(t, "control") {
		t.Fatal(inv, err)
	}
	if _, err = canonicalInventory("TestApp", "bad"); err == nil {
		t.Fatal("bad digest")
	}
}

// selectionScheduler lists the package's tasks and the install-selected
// "<pkg> executor selection" task the DSM window creates.
type selectionScheduler struct {
	schedulerFixture
	selection map[int]string
	deleted   []string
	// refuse fails the selection delete with a DSM code and keeps the task;
	// late fails it after the task is gone; blind fails every list after it.
	refuse, blind bool
	late          error
}

func selectionScript(pkg, scope string) string {
	return "#!/bin/sh\n# synopkgland executor selection\nset -eu\nexport PATH=/usr/bin:/bin\nd=/usr/local/etc/synopkgland/" + pkg + "/runtime\n" +
		`{ echo ` + strings.Repeat("a", 64) + `"  $d/executor"; echo ` + strings.Repeat("b", 64) + `"  $d/policy.json"; }|sha256sum -c - >/dev/null` + "\n" +
		`exec "$d/executor" select-scope --package '` + pkg + `' --scope '` + scope + `' --actor 'admin' --expected-generation 3` + "\n"
}

func (s *selectionScheduler) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	method, id := "", -1
	isTask := contains(args, "api=SYNO.Core.TaskScheduler")
	for _, a := range args {
		if strings.HasPrefix(a, "method=") {
			method = strings.TrimPrefix(a, "method=")
		}
		var ref struct {
			ID int `json:"id"`
		}
		if strings.HasPrefix(a, "id=") {
			json.Unmarshal([]byte(strings.TrimPrefix(a, "id=")), &ref.ID)
			id = ref.ID
		}
	}
	if !isTask {
		return s.schedulerFixture.Exec(ctx, path, args)
	}
	switch {
	case method == "list" && s.blind && len(s.deleted) > 0:
		return []byte(`{"success":false,"error":{"code":105}}`), nil
	case method == "list":
		tasks := []map[string]any{{"name": "TestApp executor control", "id": 7}}
		for n := range s.selection {
			name := "TestApp executor selection"
			if n == 12 {
				name = "OtherApp executor selection"
			}
			tasks = append(tasks, map[string]any{"name": name, "id": n})
		}
		return json.Marshal(map[string]any{"success": true, "data": map[string]any{"tasks": tasks, "total": len(tasks)}})
	case method == "get" && s.selection[id] != "":
		name := "TestApp executor selection"
		if id == 12 {
			name = "OtherApp executor selection"
		}
		return json.Marshal(map[string]any{"success": true, "data": map[string]any{"name": name, "id": id, "owner": "root", "real_owner": "root", "enable": false, "type": "script", "extra": map[string]any{"script": s.selection[id]}}})
	case method == "delete":
		for _, a := range args {
			if strings.HasPrefix(a, "tasks=") {
				s.deleted = append(s.deleted, strings.TrimPrefix(a, "tasks="))
			}
		}
		if !contains(args, `tasks=[{"id":7,"real_owner":"root"}]`) {
			if s.refuse {
				return []byte(`{"success":false,"error":{"code":4800}}`), nil
			}
			var refs []struct {
				ID int `json:"id"`
			}
			json.Unmarshal([]byte(s.deleted[len(s.deleted)-1]), &refs)
			for _, ref := range refs {
				delete(s.selection, ref.ID)
			}
			if s.late != nil {
				return nil, s.late
			}
			return []byte(`{"success":true}`), nil
		}
	}
	return s.schedulerFixture.Exec(ctx, path, args)
}

func TestPurgeDeletesTheSelectionTaskOfThisPackageOnly(t *testing.T) {
	cmd := &selectionScheduler{selection: map[int]string{
		9:  selectionScript("TestApp", "shared:media"),
		10: "#!/bin/sh\nrm -rf /\n",
		12: selectionScript("OtherApp", "home"),
	}}
	inv, err := canonicalInventory("TestApp", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "state")
	os.MkdirAll(filepath.Join(state, "runtime"), 0700)
	if err = purgeWithInventory(context.Background(), &liveDSM{Command: cmd}, "TestApp", inv, state, filepath.Join(state, "runtime"), t.TempDir(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	want := []string{`[{"id":7,"real_owner":"root"}]`, `[{"id":9,"real_owner":"root"}]`}
	if strings.Join(cmd.deleted, " ") != strings.Join(want, " ") {
		t.Fatal(cmd.deleted)
	}
}

// A selection delete is settled by listing again, and the journal says why
// the delete call failed.
func TestSelectionDeleteIsSettledByListingAgain(t *testing.T) {
	for name, tc := range map[string]struct {
		cmd  *selectionScheduler
		want string
	}{
		"deleted":          {&selectionScheduler{}, "task cleanup: selection id=9"},
		"late but deleted": {&selectionScheduler{late: context.DeadlineExceeded}, "task cleanup: selection id=9 delete_error=timeout"},
		"refused, remains": {&selectionScheduler{refuse: true}, "task cleanup failed: selection id=9 remains delete_error=dsm-4800"},
		"list unavailable": {&selectionScheduler{blind: true}, "task cleanup unconfirmed: selection id=9"},
	} {
		t.Run(name, func(t *testing.T) {
			tc.cmd.selection = map[int]string{9: selectionScript("TestApp", "shared:media"), 12: selectionScript("OtherApp", "home")}
			var logs []string
			purgeSelectionTasks(context.Background(), &liveDSM{Command: tc.cmd}, "TestApp", func(s string) { logs = append(logs, s) })
			if strings.Join(logs, "\n") != tc.want {
				t.Fatal(logs)
			}
		})
	}
}
