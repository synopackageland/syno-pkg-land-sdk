package main

import (
	"context"
	"testing"
	"time"
)

// startupFixture is an executor whose policy names a start step, with a
// runner that holds the step until release is closed.
type startupFixture struct {
	e       *Executor
	step    rootStart
	release chan struct{}
	running chan struct{}
	logs    chan string
}

func newStartupFixture(t *testing.T, start string) *startupFixture {
	t.Helper()
	e, _, _ := setup(t)
	e.Policy.Root = map[string]RootEntry{"migrate": {Digest: digestA, Kind: "script"}}
	e.Policy.Start = start
	e.PackageVersion = "1.0-1"
	f := &startupFixture{e: e, release: make(chan struct{}), running: make(chan struct{}, 1), logs: make(chan string, 16)}
	runner := &fakeRoot{run: func(ctx context.Context, _ rootCall) (int, error) {
		f.running <- struct{}{}
		select {
		case <-f.release:
			return 0, nil
		case <-ctx.Done():
			return -1, errKilled
		}
	}}
	f.step = rootStart{Approved: approved, Runner: runner, Store: e.Store}
	return f
}

func (f *startupFixture) open(t *testing.T, inventory <-chan struct{}) (func(), *int) {
	t.Helper()
	listened := 0
	listen := func() (func(), error) { listened++; return func() {}, nil }
	closeService, err := f.e.openStarted(context.Background(), listen, inventory, f.step, func(s string) { f.logs <- s })
	if err != nil {
		t.Fatal(err)
	}
	return closeService, &listened
}

func closedChannel() chan struct{} { c := make(chan struct{}); close(c); return c }

func waitFor(t *testing.T, c <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for", what)
	}
}

func waitReady(t *testing.T, e *Executor) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status := result(t, request(e, "status", nil))
		if status["ready"] == true {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatal("never ready:", status)
		}
		time.Sleep(time.Millisecond)
	}
}

// The socket opens before a due start step runs; while it runs, status says
// so and nothing else is served, so no session request precedes the step.
func TestTheServiceListensBeforeADueStartStepAndServesOnlyStatusUntilItEnds(t *testing.T) {
	f := newStartupFixture(t, "migrate")
	closeService, listened := f.open(t, closedChannel())
	defer closeService()
	if *listened != 1 {
		t.Fatal("the service did not listen before the start step:", *listened)
	}
	waitFor(t, f.running, "the start step")
	status := result(t, request(f.e, "status", nil))
	if status["ready"] != false || status["state"] != "starting" || status["executor_digest"] != f.e.ExecutorDigest || status["policy_digest"] != f.e.PolicyDigest {
		t.Fatal(status)
	}
	if out := request(f.e, "root.jobs.list", nil); out.Code != "EXECUTOR_STARTING" {
		t.Fatal(out)
	}
	close(f.release)
	status = waitReady(t, f.e)
	if _, ok := status["state"]; ok {
		t.Fatal("a started executor still reports a state:", status)
	}
	if got := status["start_step"].(map[string]any)["state"]; got != "ok" {
		t.Fatal(got)
	}
	if out := request(f.e, "root.jobs.list", nil); out.Code == "EXECUTOR_STARTING" {
		t.Fatal(out)
	}
}

// The start step proves its approval with the task inventory, which the
// executor reads back from DSM after it listens: the step waits for it.
func TestADueStartStepWaitsForTheTaskInventory(t *testing.T) {
	f := newStartupFixture(t, "migrate")
	inventory := make(chan struct{})
	closeService, _ := f.open(t, inventory)
	defer closeService()
	select {
	case <-f.running:
		t.Fatal("the start step ran before the task inventory")
	case <-time.After(50 * time.Millisecond):
	}
	if status := result(t, request(f.e, "status", nil)); status["state"] != "starting" {
		t.Fatal(status)
	}
	close(inventory)
	waitFor(t, f.running, "the start step")
	close(f.release)
	waitReady(t, f.e)
}

// Without a step due, the service is ready as soon as it listens; the task
// inventory and the version record follow in the background.
func TestWithoutADueStartStepTheServiceIsReadyWhenItListens(t *testing.T) {
	for name, f := range map[string]*startupFixture{
		"no start step": newStartupFixture(t, ""),
		"already ran":   newStartupFixture(t, "migrate"),
	} {
		if name == "already ran" {
			if err := f.e.Store.SetStartRecord(startRecord{Version: "1.0-1", Step: &startStepResult{Exit: "0"}}); err != nil {
				t.Fatal(err)
			}
		}
		inventory := make(chan struct{})
		closeService, _ := f.open(t, inventory)
		if status := result(t, request(f.e, "status", nil)); status["ready"] != true {
			t.Fatal(name, status)
		}
		if out := request(f.e, "root.jobs.list", nil); out.Code == "EXECUTOR_STARTING" {
			t.Fatal(name, out)
		}
		close(inventory)
		// Closing the service as the readback ends may cancel the record; wait for it first.
		waitRecorded(t, f.e, name)
		closeService()
	}
}

// waitRecorded waits for the start that follows the readback to record the package version.
func waitRecorded(t *testing.T, e *Executor, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, err := e.Store.StartRecord()
		if err == nil && r.Version == e.PackageVersion {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(name, "version not recorded:", r, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Closing the service, for a package stop, interrupts a running start step;
// it is not recorded and runs at the next start.
func TestClosingTheServiceInterruptsTheStartStep(t *testing.T) {
	f := newStartupFixture(t, "migrate")
	closeService, _ := f.open(t, closedChannel())
	waitFor(t, f.running, "the start step")
	closed := make(chan struct{})
	go func() { closeService(); close(closed) }()
	waitFor(t, closed, "the service to close")
	if _, err := f.e.Store.StartRecord(); err == nil {
		t.Fatal("an interrupted start step was recorded")
	}
}
