package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// fakeTaskCheck is a task check on a clock the test moves; each readback
// takes took and answers fail.
type fakeTaskCheck struct {
	*taskCheck
	now   time.Time
	took  time.Duration
	fail  error
	reads int
}

func newFakeTaskCheck() *fakeTaskCheck {
	f := &fakeTaskCheck{now: time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC)}
	f.taskCheck = &taskCheck{Now: func() time.Time { return f.now }, Check: func(context.Context) error {
		f.reads++
		f.now = f.now.Add(f.took)
		return f.fail
	}}
	return f
}

func checkState(t *testing.T, c *taskCheck) (string, string) {
	t.Helper()
	b, _ := json.Marshal(c.Status())
	var out struct {
		State     string `json:"state"`
		CheckedAt string `json:"checked_at"`
	}
	if json.Unmarshal(b, &out) != nil {
		t.Fatalf("tasks: %s", b)
	}
	return out.State, out.CheckedAt
}

func TestTaskCheckReportsCheckingThenTheReadbackItsStartTimeAndFailure(t *testing.T) {
	f := newFakeTaskCheck()
	if state, at := checkState(t, f.taskCheck); state != "checking" || at != "" {
		t.Fatalf("before a readback: %s %q", state, at)
	}
	f.took = 3 * time.Second
	f.run(context.Background())
	if state, at := checkState(t, f.taskCheck); state != "ready" || at != "2026-10-15T04:00:00Z" {
		t.Fatalf("passed: %s %q", state, at)
	}
	f.fail = errors.New("task script mismatch")
	f.run(context.Background())
	if state, at := checkState(t, f.taskCheck); state != "failed" || at != "2026-10-15T04:00:03Z" {
		t.Fatalf("failed: %s %q", state, at)
	}
}

// A readback that passed stays ready for one period and a slow readback,
// then reads stale; the boundary is swept second by second.
func TestTaskCheckIsReadyUntilItsFreshnessEndsThenStale(t *testing.T) {
	for offset := taskCheckFresh - 3*time.Second; offset <= taskCheckFresh+3*time.Second; offset += time.Second {
		f := newFakeTaskCheck()
		f.run(context.Background())
		f.now = f.now.Add(offset)
		f.Check = func(context.Context) error { return nil } // a stale status starts one; this test reads only its state
		want := "ready"
		if offset > taskCheckFresh {
			want = "stale"
		}
		if state, _ := checkState(t, f.taskCheck); state != want {
			t.Fatalf("%v after the readback: %s, want %s", offset, state, want)
		}
	}
}

// Phase sweep: the loop reads back every period after a pass and every
// retry after a failure. Whatever a readback takes up to taskCheckSlow, and
// at every 10 s phase of the readbacks and the waits between them, a run of
// passing readbacks never reads stale once the first one ended.
func TestTaskCheckLoopStaysFreshAcrossReadbackDurationsAndStatusPhases(t *testing.T) {
	const step = 10 * time.Second
	for took := time.Duration(0); took <= taskCheckSlow; took += 15 * time.Second {
		f := newFakeTaskCheck()
		ended := false
		sample := func(at string) {
			want := "checking"
			if ended {
				want = "ready"
			}
			if state, _ := checkState(t, f.taskCheck); state != want {
				t.Fatalf("readback takes %v, %s: %s, want %s", took, at, state, want)
			}
		}
		f.Check = func(context.Context) error {
			for d := time.Duration(0); d < took; d += step {
				f.now = f.now.Add(min(step, took-d))
				sample("during a readback")
			}
			return nil
		}
		for round := 0; round < 4; round++ {
			f.run(context.Background())
			ended = true
			wait := f.next()
			if wait != taskCheckEvery {
				t.Fatalf("after a pass wait %v", wait)
			}
			for d := time.Duration(0); d < wait; d += step {
				f.now = f.now.Add(step)
				sample("between readbacks")
			}
		}
	}
	f := newFakeTaskCheck()
	f.fail = errors.New("dsm unavailable")
	f.run(context.Background())
	if wait := f.next(); wait != taskCheckRetry {
		t.Fatalf("after a failure wait %v", wait)
	}
}

// A stale result whose loop did not read back (a suspended NAS) starts a
// readback from status, one at a time.
func TestTaskCheckStatusStartsOneReadbackWhenStale(t *testing.T) {
	f := newFakeTaskCheck()
	f.run(context.Background())
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	f.Check = func(context.Context) error { started <- struct{}{}; <-release; return nil }
	f.Base = context.Background()
	if state, _ := checkState(t, f.taskCheck); state != "ready" {
		t.Fatal("fresh")
	}
	f.now = f.now.Add(taskCheckFresh + time.Second)
	for i := 0; i < 3; i++ {
		if state, _ := checkState(t, f.taskCheck); state != "stale" {
			t.Fatalf("stale: %s", state)
		}
	}
	<-started
	select {
	case <-started:
		t.Fatal("a second readback started while one runs")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for deadline := time.Now().Add(5 * time.Second); ; {
		if state, _ := checkState(t, f.taskCheck); state == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stale readback did not land")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStatusReportsTheExecutorsTaskCheckOnlyWhenItHasOne(t *testing.T) {
	e, _, _ := setup(t)
	r := request(e, "status", map[string]any{})
	if _, ok := r.Result.(map[string]any)["tasks"]; !r.OK || ok {
		t.Fatalf("without a task check: %#v", r.Result)
	}
	f := newFakeTaskCheck()
	f.run(context.Background())
	e.TaskCheck = f.taskCheck
	r = request(e, "status", map[string]any{})
	tasks, _ := r.Result.(map[string]any)["tasks"].(map[string]any)
	if !r.OK || tasks["state"] != "ready" || tasks["checked_at"] != "2026-10-15T04:00:00Z" {
		t.Fatalf("with a task check: %#v", r.Result)
	}
}
