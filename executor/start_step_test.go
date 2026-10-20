package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func startPolicy() Policy {
	p := purgePolicy("Demo")
	p.Root["migrate"] = RootEntry{Digest: strings.Repeat("d", 64), Kind: "script"}
	p.Start = "migrate"
	return p
}

func TestPolicyNamesOneOfItsRootEntriesAsTheStartStep(t *testing.T) {
	if err := validatePolicy(startPolicy(), "Demo"); err != nil {
		t.Fatal(err)
	}
	p := startPolicy()
	p.Start = "missing"
	if validatePolicy(p, "Demo") == nil {
		t.Fatal("a start step outside root accepted")
	}
	p = startPolicy()
	p.Root["migrate"] = RootEntry{Digest: strings.Repeat("d", 64), Kind: "script", Run: &RootRun{}}
	if err := validatePolicy(p, "Demo"); err == nil || !strings.Contains(err.Error(), "policy start") {
		t.Fatal("a start step a session may run accepted:", err)
	}
}

func TestSessionsCannotRunTheStartStep(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"migrate":{"digest":"`+digestA+`","run":{},"job":{}}}`)
	e.Policy.Start = "migrate"
	e.startJobs()
	defer e.stopJobs("executor-stop")
	for _, op := range []string{"root.run", "root.start"} {
		if out := request(e, op, map[string]any{"name": "migrate"}); out.Code != "POLICY_DENIED" {
			t.Fatal(op, out)
		}
	}
	if len(r.started()) != 0 {
		t.Fatal("the start step ran from a session")
	}
}

// startOnce runs the start step for version with a runner that prints out
// and exits with code, and answers the runner and the log.
func startOnce(t *testing.T, store *Store, p Policy, version string, approval func(context.Context) error, code int) (*purgeRunnerStub, []string) {
	t.Helper()
	r := &purgeRunnerStub{out: "moved\n", code: code}
	var logs []string
	rootStart{Approved: approval, Runner: r, Store: store}.run(context.Background(), p, version, func(s string) { logs = append(logs, s) })
	return r, logs
}

func TestTheStartStepRunsOnceAfterAnInstallAndOnceAfterEachUpgrade(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	r, logs := startOnce(t, store, startPolicy(), "1.0-1", approved, 0)
	if r.calls != 1 || r.call.Name != "migrate" || r.call.Mode != "start" || !reflect.DeepEqual(r.call.Args, []string{"install"}) || env(r.call)["SYNOPKGLAND_ROOT_MODE"] != "start" {
		t.Fatal(r.call)
	}
	if !reflect.DeepEqual(logs, []string{`start root step: package=Demo exit=0 output="moved\n"`}) {
		t.Fatal(logs)
	}
	if r, _ = startOnce(t, store, startPolicy(), "1.0-1", approved, 0); r.calls != 0 {
		t.Fatal("the start step ran again for the same version")
	}
	if r, _ = startOnce(t, store, startPolicy(), "1.0-2", approved, 0); r.calls != 1 || !reflect.DeepEqual(r.call.Args, []string{"upgrade", "1.0-1"}) {
		t.Fatal(r.calls, r.call.Args)
	}
}

// A failed step is the result for that version: it is logged and recorded,
// and the next start of the same version does not repeat it.
func TestAFailedStartStepIsRecordedAndNotRepeated(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	_, logs := startOnce(t, store, startPolicy(), "1.0-1", approved, 3)
	if !reflect.DeepEqual(logs, []string{`start root step failed: package=Demo exit=3 output="moved\n"`}) {
		t.Fatal(logs)
	}
	got := startStepStatus(store, "1.0-1")
	if got["state"] != "failed" || got["exit"] != "3" || got["reason"] != "install" || got["output"] != "moved\n" {
		t.Fatal(got)
	}
	if r, _ := startOnce(t, store, startPolicy(), "1.0-1", approved, 0); r.calls != 0 {
		t.Fatal("a failed start step ran again for the same version")
	}
}

// Without a live approval the step waits: it runs at the first start that
// has one, still as the step of that install or upgrade.
func TestAnUnapprovedStartStepWaitsForTheNextStart(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	startOnce(t, store, startPolicy(), "1.0-1", approved, 0)
	r, logs := startOnce(t, store, startPolicy(), "1.0-2", func(context.Context) error { return os.ErrPermission }, 0)
	if r.calls != 0 || !reflect.DeepEqual(logs, []string{"start root step skipped: not approved: package=Demo"}) {
		t.Fatal(r.calls, logs)
	}
	if got := startStepStatus(store, "1.0-2"); !reflect.DeepEqual(got, map[string]any{"state": "pending"}) {
		t.Fatal(got)
	}
	if r, _ = startOnce(t, store, startPolicy(), "1.0-2", approved, 0); r.calls != 1 || !reflect.DeepEqual(r.call.Args, []string{"upgrade", "1.0-1"}) {
		t.Fatal(r.calls, r.call.Args)
	}
}

// Every start records the package version, so a step a later version adds
// sees that version's upgrade.
func TestAPolicyWithoutAStartStepStillRecordsTheVersion(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	p := startPolicy()
	p.Start = ""
	if r, _ := startOnce(t, store, p, "1.0-1", approved, 0); r.calls != 0 {
		t.Fatal("a policy without a start step ran one")
	}
	if r, _ := startOnce(t, store, startPolicy(), "1.0-2", approved, 0); r.calls != 1 || !reflect.DeepEqual(r.call.Args, []string{"upgrade", "1.0-1"}) {
		t.Fatal(r.calls, r.call.Args)
	}
}

// An executor stopped mid-step records nothing, so the step runs again.
func TestAnInterruptedStartStepRunsAgain(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	r := &purgeRunnerStub{block: true, during: cancel}
	rootStart{Approved: approved, Runner: r, Store: store}.run(ctx, startPolicy(), "1.0-1", func(string) {})
	if r.calls != 1 {
		t.Fatal(r.calls)
	}
	if r, _ := startOnce(t, store, startPolicy(), "1.0-1", approved, 0); r.calls != 1 || !reflect.DeepEqual(r.call.Args, []string{"install"}) {
		t.Fatal("an interrupted start step was recorded")
	}
}

func TestStatusReportsTheStartStepOfThisVersion(t *testing.T) {
	e, _, _ := setup(t)
	if _, ok := result(t, request(e, "status", nil))["start_step"]; ok {
		t.Fatal("a policy without a start step reports one")
	}
	e.Policy.Start = "migrate"
	e.PackageVersion = "1.0-2"
	if got := result(t, request(e, "status", nil))["start_step"]; !reflect.DeepEqual(got, map[string]any{"state": "pending"}) {
		t.Fatal(got)
	}
	at := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	if err := e.Store.SetStartRecord(startRecord{Version: "1.0-2", Step: &startStepResult{Reason: "upgrade", PreviousVersion: "1.0-1", Exit: "0", Output: "moved\n", FinishedAt: at}}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"state": "ok", "reason": "upgrade", "previous_version": "1.0-1", "exit": "0", "output": "moved\n", "finished_at": "2026-10-14T08:00:00Z"}
	if got := result(t, request(e, "status", nil))["start_step"]; !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	e.PackageVersion = "1.0-3"
	if got := result(t, request(e, "status", nil))["start_step"]; !reflect.DeepEqual(got, map[string]any{"state": "pending"}) {
		t.Fatal(got)
	}
}

func TestThePackageVersionComesFromINFO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "INFO")
	for content, want := range map[string]string{
		"package=\"Demo\"\nversion=\"1.2.0-0042\"\n": "1.2.0-0042",
		"version=1.0\n":                  "1.0",
		"package=\"Demo\"\n":             "",
		"version=\"a b\"\n":              "",
		"version=\"1\"\nversion=\"2\"\n": "",
	} {
		os.WriteFile(path, []byte(content), 0644)
		if got := packageVersion(path); got != want {
			t.Fatal(content, got)
		}
	}
}
