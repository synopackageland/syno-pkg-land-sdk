package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dsm71Scheduler answers like DSM 7.1.1-42962 (arm-nas.example): TaskScheduler stops at
// list v2 (offset/limit) and get v3. the regression test measured the refusals: a version
// above maxVersion 3 returns 104; a method absent at an existing version, 103.
type dsm71Scheduler struct {
	recordingScheduler
	refusal string
}

func (s *dsm71Scheduler) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	api, method, version := schedulerCall(args)
	s.calls = append(s.calls, api+" "+method+" v"+version)
	if api == "SYNO.Core.TaskScheduler" {
		if method == "list" && version == "3" && s.refusal != "" {
			return []byte(`{"success":false,"error":{"code":` + s.refusal + `}}`), nil
		}
		supported := map[string]string{"list": "2", "get": "3", "delete": "2", "run": "2"}
		if supported[method] != version {
			if version > "3" {
				return []byte(`{"success":false,"error":{"code":104}}`), nil
			}
			return []byte(`{"success":false,"error":{"code":103}}`), nil
		}
		if method == "list" && (!contains(args, "offset=0") || !contains(args, "limit=500")) {
			return []byte(`{"success":false,"error":{"code":101}}`), nil
		}
	}
	return s.schedulerFixture.Exec(ctx, path, args)
}

func purgeWith(t *testing.T, cmd Command) {
	t.Helper()
	inventory := taskInventory{Package: "TestApp", PolicyDigest: strings.Repeat("b", 64), Tasks: []approvedTask{{Kind: "boot", Name: "TestApp executor boot", ScriptDigest: canonicalTaskDigest(t, "boot")}, {Kind: "control", Name: "TestApp executor control", ScriptDigest: canonicalTaskDigest(t, "control")}}}
	state := filepath.Join(t.TempDir(), "state")
	socket := filepath.Join(t.TempDir(), "socket")
	os.MkdirAll(filepath.Join(state, "runtime"), 0700)
	os.MkdirAll(socket, 0700)
	if err := purgeWithInventory(context.Background(), &liveDSM{Command: cmd}, "TestApp", inventory, state, filepath.Join(state, "runtime"), socket, func(string) {}); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeFindsAndDeletesBothTasksOnDSM71(t *testing.T) {
	cmd := &dsm71Scheduler{}
	purgeWith(t, cmd)
	if strings.Join(cmd.deleted, ",") != "SYNO.Core.EventScheduler,SYNO.Core.TaskScheduler" {
		t.Fatal("DSM 7.1 tasks left behind", cmd.deleted, cmd.calls)
	}
}

func TestSchedulerReadFallsBackOnlyOnUnsupportedVersion(t *testing.T) {
	for _, code := range []string{"105", "119", "102", "3121"} {
		t.Run(code, func(t *testing.T) {
			cmd := &dsm71Scheduler{refusal: code}
			purgeWith(t, cmd)
			for _, call := range cmd.calls {
				if call == "SYNO.Core.TaskScheduler list v2" || call == "SYNO.Core.TaskScheduler get v3" {
					t.Fatal("fell back after code", code, cmd.calls)
				}
			}
			if contains(cmd.deleted, "SYNO.Core.TaskScheduler") {
				t.Fatal("deleted without exact readback", cmd.deleted)
			}
		})
	}
}

func TestModernSchedulerNeverUsesLegacyVersions(t *testing.T) {
	cmd := &recordingScheduler{}
	purgeWith(t, cmd)
	for _, call := range cmd.calls {
		if call == "SYNO.Core.TaskScheduler list v2" || call == "SYNO.Core.TaskScheduler get v3" {
			t.Fatal("modern DSM used a legacy version", cmd.calls)
		}
	}
	if len(cmd.deleted) != 2 {
		t.Fatal(cmd.deleted)
	}
}

type recordingScheduler struct {
	schedulerFixture
	calls []string
}

func (s *recordingScheduler) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	api, method, version := schedulerCall(args)
	s.calls = append(s.calls, api+" "+method+" v"+version)
	return s.schedulerFixture.Exec(ctx, path, args)
}

func schedulerCall(args []string) (api, method, version string) {
	for _, a := range args {
		k, v, _ := strings.Cut(a, "=")
		switch k {
		case "api":
			api = v
		case "method":
			method = v
		case "version":
			version = v
		}
	}
	return
}
