package main

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func stopPolicy() Policy {
	p := purgePolicy("Demo")
	p.Root["stop"] = RootEntry{Digest: strings.Repeat("c", 64), Kind: "script"}
	p.Stop = "stop"
	return p
}

func TestPolicyNamesOneOfItsRootEntriesAsTheStopStep(t *testing.T) {
	if err := validatePolicy(stopPolicy(), "Demo"); err != nil {
		t.Fatal(err)
	}
	p := stopPolicy()
	p.Stop = "missing"
	if validatePolicy(p, "Demo") == nil {
		t.Fatal("a stop step outside root accepted")
	}
}

func TestSessionsCannotRunTheStopStep(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"stop":{"digest":"`+digestA+`","run":{},"job":{}}}`)
	e.Policy.Stop = "stop"
	e.startJobs()
	defer e.stopJobs("executor-stop")
	for _, op := range []string{"root.run", "root.start"} {
		if out := request(e, op, map[string]any{"name": "stop"}); out.Code != "POLICY_DENIED" {
			t.Fatal(op, out)
		}
	}
	if len(r.started()) != 0 {
		t.Fatal("the stop step ran from a session")
	}
}

func TestTheStopStepGetsStopOrPrepareUpgrade(t *testing.T) {
	for reason, arg := range map[string]string{"stop": "stop", "upgrade": "prepare-upgrade"} {
		r := &purgeRunnerStub{out: "flushed\n"}
		var logs []string
		rootStop{Approved: approved, Runner: r}.run(context.Background(), stopPolicy(), reason, func(s string) { logs = append(logs, s) })
		if r.calls != 1 || r.call.Name != "stop" || r.call.Mode != "stop" || !reflect.DeepEqual(r.call.Args, []string{arg}) || env(r.call)["SYNOPKGLAND_ROOT_MODE"] != "stop" {
			t.Fatal(reason, r.call)
		}
		if !reflect.DeepEqual(logs, []string{`stop root step: package=Demo exit=0 output="flushed\n"`}) {
			t.Fatal(logs)
		}
	}
	r := &purgeRunnerStub{}
	var logs []string
	rootStop{Approved: func(context.Context) error { return os.ErrPermission }, Runner: r}.run(context.Background(), stopPolicy(), "stop", func(s string) { logs = append(logs, s) })
	if r.calls != 0 || !reflect.DeepEqual(logs, []string{"stop root step skipped: not approved: package=Demo"}) {
		t.Fatal(logs)
	}
	p := stopPolicy()
	p.Stop = ""
	rootStop{Approved: approved, Runner: r}.run(context.Background(), p, "stop", func(string) {})
	if r.calls != 0 {
		t.Fatal("a policy without a stop step ran one")
	}
}

func TestTheStopReasonComesFromThePackage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stop-reason")
	if stopReason(path) != "stop" {
		t.Fatal("a missing answer is a plain stop")
	}
	for content, want := range map[string]string{"upgrade\n": "upgrade", "stop\n": "stop", "purge\n": "stop", "upgrade-now\n": "stop"} {
		os.WriteFile(path, []byte(content), 0600)
		if got := stopReason(path); got != want {
			t.Fatal(content, got)
		}
	}
}

// A package stop first ends the jobs, then runs the stop step; an executor
// that ends for another reason runs no stop step.
func TestEndingTheRootWorkCancelsJobsBeforeTheStopStep(t *testing.T) {
	var order []string
	e, _, _ := jobSetup(t, cleanPolicy(""), func(_ context.Context, _ rootCall, control *bufio.Reader, _ io.Writer) (int, error) {
		line, _ := control.ReadString('\n')
		order = append(order, strings.TrimSpace(line))
		return 0, nil
	})
	e.StopStep = func(reason string) { order = append(order, "step "+reason) }
	startJob(t, e, nil)
	e.endRoot("upgrade")
	if !reflect.DeepEqual(order, []string{`{"cancel":"upgrade"}`, "step upgrade"}) {
		t.Fatal(order)
	}
	order = nil
	e.startJobs()
	e.endRoot("executor-stop")
	e.endRoot("purge")
	if len(order) != 0 {
		t.Fatal(order)
	}
}
