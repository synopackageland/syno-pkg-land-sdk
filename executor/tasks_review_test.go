package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewSnapshotRejectsUnrelatedSameNamedRootTasks(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ed, pd := strings.Repeat("a", 64), strings.Repeat("b", 64)
	tasks := []map[string]string{}
	for _, kind := range []string{"boot", "control"} {
		header := "#!/bin/sh\n# synopkgland executor " + kind + "\n"
		// Another admin's script merely happens to carry these marker comments.
		script := header + "# " + ed + " " + pd + "\necho unrelated-backup-task\n"
		tasks = append(tasks, map[string]string{"kind": kind, "name": "TestApp executor " + kind, "header": header, "script": script})
	}
	setup := map[string]any{"package": "TestApp", "executorDigest": ed, "policyDigest": pd, "implementation": "executor", "tasks": tasks}
	b, _ := json.Marshal(setup)
	path := filepath.Join(dir, "setup.json")
	os.WriteFile(path, b, 0600)
	err = snapshotTasks(context.Background(), &liveDSM{Command: &snapshotFixture{tasks: tasks}}, "TestApp", ed, pd, path, dir)
	if err == nil {
		t.Fatal("unrelated root tasks accepted into protected deletion inventory")
	}
}

func TestReviewPurgeRejectsUnrelatedSameNamedRootTasks(t *testing.T) {
	script := "#!/bin/sh\n# synopkgland executor boot\n# " + strings.Repeat("a", 64) + strings.Repeat("b", 64) + "\necho unrelated-backup-task\n"
	cmd := &schedulerFixture{script: script}
	inv := taskInventory{Package: "TestApp", PolicyDigest: strings.Repeat("b", 64), Tasks: []approvedTask{{Kind: "boot", Name: "TestApp executor boot", ScriptDigest: hashBytes([]byte(script))}, {Kind: "control", Name: "TestApp executor control", ScriptDigest: hashBytes([]byte(script))}}}
	purgeTasks(context.Background(), &liveDSM{Command: cmd}, "TestApp", inv, func(string) {})
	if len(cmd.deleted) != 0 {
		t.Fatal("deleted unrelated tasks", cmd.deleted)
	}
}
