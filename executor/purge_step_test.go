package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func purgePolicy(pkg string) Policy {
	return Policy{Version: 1, Package: pkg, App: "SYNO.SDS." + pkg + ".Instance", Operations: []string{"status", "root.run"},
		Root: map[string]RootEntry{"cleanup": rootDigests(map[string]string{"cleanup": strings.Repeat("a", 64)})["cleanup"], "purge": {Digest: strings.Repeat("b", 64), Kind: "script"}}, Purge: "purge"}
}

func TestPolicyNamesOneOfItsRootOpsAsThePurgeStep(t *testing.T) {
	if err := validatePolicy(purgePolicy("Demo"), "Demo"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Policy){
		"unknown op":   func(p *Policy) { p.Purge = "other" },
		"invalid name": func(p *Policy) { p.Purge = "a/b" },
		"no root ops":  func(p *Policy) { p.Root = nil },
	} {
		p := purgePolicy("Demo")
		mutate(&p)
		if validatePolicy(p, "Demo") == nil {
			t.Fatal(name, "accepted")
		}
	}
}

// The purge op runs only at uninstall; a session cannot start it early.
func TestRootRunRefusesThePurgeStep(t *testing.T) {
	e, f, _ := setup(t)
	f.user.Admin = true
	r := &fakeRoot{}
	e.Runner = r
	e.Policy.Root = rootDigests(map[string]string{"whoami": strings.Repeat("a", 64), "purge": strings.Repeat("b", 64)})
	e.Policy.Purge = "purge"
	if out := request(e, "root.run", map[string]any{"name": "purge"}); out.Code != "POLICY_DENIED" || len(r.started()) != 0 {
		t.Fatal(out)
	}
	if out := request(e, "root.run", map[string]any{"name": "whoami"}); !out.OK || len(r.started()) != 1 {
		t.Fatal(out)
	}
}

// purgeRunnerStub is a root runner whose program prints out and ends with
// code, or err as Start's or Wait's failure.
type purgeRunnerStub struct {
	calls         int
	call          rootCall
	out           string
	code          int
	startErr, err error
	during        func()
	block         bool
}

func (r *purgeRunnerStub) Start(c rootCall) (rootProcess, error) {
	r.calls++
	r.call = c
	if r.startErr != nil {
		return nil, r.startErr
	}
	f := &fakeRoot{run: func(ctx context.Context, c rootCall) (int, error) {
		if r.during != nil {
			r.during()
		}
		io.WriteString(c.Stdout, r.out)
		if r.block {
			<-ctx.Done()
			return -1, errKilled
		}
		return r.code, r.err
	}}
	return f.Start(c)
}

func approved(context.Context) error { return nil }

func runPurgeStep(step rootPurge, policy Policy) []string {
	var logs []string
	step.run(context.Background(), policy, func(s string) { logs = append(logs, s) })
	return logs
}

func TestRootPurgeStepRunsTheApprovedOpForSixtySeconds(t *testing.T) {
	for deleteState, args := range map[bool][]string{false: {"keep-state"}, true: {"delete-state"}} {
		r := &purgeRunnerStub{out: "released\n"}
		logs := runPurgeStep(rootPurge{Approved: approved, Runner: r, DeleteState: deleteState}, purgePolicy("Demo"))
		if r.calls != 1 || r.call.Name != "purge" || r.call.Entry.Digest != strings.Repeat("b", 64) || r.call.Mode != "purge" || !reflect.DeepEqual(r.call.Args, args) || !reflect.DeepEqual(env(r.call), map[string]string{"SYNOPKGLAND_PACKAGE": "Demo", "SYNOPKGLAND_ROOT_MODE": "purge"}) {
			t.Fatal(deleteState, r)
		}
		if len(logs) != 1 || logs[0] != `purge root step: package=Demo exit=0 output="released\n"` {
			t.Fatal(logs)
		}
	}
}

func TestRootPurgeStepWithoutLiveApprovalIsSkippedAndLogged(t *testing.T) {
	for name, check := range map[string]func(context.Context) error{
		"offline":    nil,
		"not proved": func(context.Context) error { return errors.New("task script mismatch") },
	} {
		r := &purgeRunnerStub{}
		logs := runPurgeStep(rootPurge{Approved: check, Runner: r, DeleteState: true}, purgePolicy("Demo"))
		if r.calls != 0 || len(logs) != 1 || logs[0] != "purge root step skipped: not approved: package=Demo" {
			t.Fatal(name, r.calls, logs)
		}
	}
}

func TestPolicyWithoutAPurgeStepRunsAndLogsNothing(t *testing.T) {
	p := purgePolicy("Demo")
	p.Purge = ""
	r := &purgeRunnerStub{}
	if logs := runPurgeStep(rootPurge{Approved: approved, Runner: r}, p); r.calls != 0 || len(logs) != 0 {
		t.Fatal(r.calls, logs)
	}
}

func TestAFailedRootPurgeStepIsOnlyLogged(t *testing.T) {
	signal := exec.Command("/bin/sh", "-c", "kill -9 $$").Run()
	for want, r := range map[string]*purgeRunnerStub{
		`purge root step failed: package=Demo exit=3 output="partial"`:      {out: "partial", code: 3},
		`purge root step failed: package=Demo exit=signal output="partial"`: {out: "partial", code: -1, err: signal},
		`purge root step failed: package=Demo exit=refused output=""`:       {startErr: errors.New("digest")},
	} {
		if logs := runPurgeStep(rootPurge{Approved: approved, Runner: r}, purgePolicy("Demo")); len(logs) != 1 || logs[0] != want {
			t.Fatal(logs)
		}
	}
}

func TestLongRootPurgeOutputIsCutInTheLog(t *testing.T) {
	r := &purgeRunnerStub{out: strings.Repeat("x", 5000)}
	logs := runPurgeStep(rootPurge{Approved: approved, Runner: r}, purgePolicy("Demo"))
	if len(logs) != 1 || !strings.HasSuffix(logs[0], `x…"`) || len(logs[0]) > 4200 {
		t.Fatal(len(logs[0]))
	}
}

func TestARootPurgeStepPastSixtySecondsIsKilled(t *testing.T) {
	r := &purgeRunnerStub{out: "partial", block: true}
	exit, out := runStep(context.Background(), r, purgePolicy("Demo"), "purge", "purge", []string{"keep-state"}, "", 10*time.Millisecond)
	if exit != "timeout" || out != "partial" || purgeStepTimeout != time.Minute {
		t.Fatal(exit, out)
	}
}

// The step proves consent with the approval tasks, so it runs while they and
// the protected state still exist.
func TestPurgeRunsTheRootStepBeforeItRemovesTasksAndState(t *testing.T) {
	state, runtime := purgeFixture(t, purgePolicy("TestApp"))
	r := &purgeRunnerStub{}
	r.during = func() {
		if _, err := os.Stat(runtime + "/policy.json"); err != nil {
			t.Error("state removed before the root step", err)
		}
	}
	cmd := &leftoverDSM{}
	if err := purgeProtectedWith(context.Background(), &liveDSM{Command: cmd}, "TestApp", state, runtime, t.TempDir(), leftoverFixture(t, false), rootPurge{Approved: approved, Runner: r}); err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 {
		t.Fatal("root step did not run")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("state kept", err)
	}
}

func approvedInventory(t *testing.T) taskInventory {
	return taskInventory{Package: "TestApp", PolicyDigest: strings.Repeat("b", 64), Tasks: []approvedTask{{Kind: "boot", Name: "TestApp executor boot", ScriptDigest: canonicalTaskDigest(t, "boot")}, {Kind: "control", Name: "TestApp executor control", ScriptDigest: canonicalTaskDigest(t, "control")}}}
}

// Consent at uninstall is the approval tasks of exactly this policy: both
// must still be in the scheduler with their pinned scripts.
func TestLiveApprovalNeedsBothExactTasksOfThisPolicy(t *testing.T) {
	pd := strings.Repeat("b", 64)
	if err := liveTasks(context.Background(), &liveDSM{Command: &schedulerFixture{}}, "TestApp", pd, approvedInventory(t)); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		cmd    *schedulerFixture
		digest string
		edit   func(*taskInventory)
	}{
		"script changed": {cmd: &schedulerFixture{changed: true}},
		"owner changed":  {cmd: &schedulerFixture{foreignOwner: true}},
		"duplicate task": {cmd: &schedulerFixture{duplicate: true}},
		"other policy":   {cmd: &schedulerFixture{}, digest: strings.Repeat("c", 64)},
		"invalid":        {cmd: &schedulerFixture{}, edit: func(inv *taskInventory) { inv.Tasks = inv.Tasks[:1] }},
		"other package":  {cmd: &schedulerFixture{}, edit: func(inv *taskInventory) { inv.Package = "OtherApp" }},
	} {
		inv := approvedInventory(t)
		if c.edit != nil {
			c.edit(&inv)
		}
		digest := pd
		if c.digest != "" {
			digest = c.digest
		}
		if liveTasks(context.Background(), &liveDSM{Command: c.cmd}, "TestApp", digest, inv) == nil {
			t.Fatal(name, "approved")
		}
		if len(c.cmd.deleted) != 0 {
			t.Fatal(name, "changed the scheduler")
		}
	}
}

// The package account writes the admin's answer next to the purge mailbox;
// only the exact request deletes state.
func TestUninstallDeletesStateOnlyOnTheExactRequest(t *testing.T) {
	dir := realTestPath(t, t.TempDir())
	path := dir + "/uninstall"
	if uninstallDeletesState(path) {
		t.Fatal("missing answer deletes state")
	}
	for content, want := range map[string]bool{"delete-state\n": true, "delete-state": true, "keep-state\n": false, "delete-state\n\n": false, " delete-state": false, strings.Repeat("x", 100): false} {
		os.WriteFile(path, []byte(content), 0600)
		if uninstallDeletesState(path) != want {
			t.Fatalf("%q", content)
		}
	}
	os.Remove(path)
	os.WriteFile(dir+"/target", []byte("delete-state\n"), 0600)
	os.Symlink(dir+"/target", path)
	if uninstallDeletesState(path) {
		t.Fatal("symlink followed")
	}
}
