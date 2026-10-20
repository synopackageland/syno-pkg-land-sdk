package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

type superviseFixture struct {
	scripts []string
	revived []bool
	delays  []time.Duration
	present bool
	clock   time.Time
	runFor  []time.Duration
}

// Each run executes a real child so exit status and signal death come from the
// kernel, exactly as the supervisor sees serve.
func (f *superviseFixture) run(ctx context.Context, revived bool) error {
	i := len(f.revived)
	f.revived = append(f.revived, revived)
	if i < len(f.runFor) {
		f.clock = f.clock.Add(f.runFor[i])
	}
	script := "exit 0"
	if i < len(f.scripts) {
		script = f.scripts[i]
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", script).Run()
}
func (f *superviseFixture) sleep(_ context.Context, d time.Duration) bool {
	f.delays = append(f.delays, d)
	return true
}
func (f *superviseFixture) supervise(ctx context.Context) error {
	return supervise(ctx, f.run, func() bool { return f.present }, f.sleep, func() time.Time { return f.clock }, func(string) {})
}

func TestSuperviseRestartsAbnormalServeUntilCleanExit(t *testing.T) {
	f := &superviseFixture{present: true, scripts: []string{"kill -KILL $$", "exit 1", "exit 0"}}
	if err := f.supervise(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []bool{false, true, true}; !reflect.DeepEqual(f.revived, want) {
		t.Fatalf("revived %v, want %v", f.revived, want)
	}
	if want := []time.Duration{time.Second, 2 * time.Second}; !reflect.DeepEqual(f.delays, want) {
		t.Fatalf("delays %v, want %v", f.delays, want)
	}
}

func TestSuperviseLeavesWhenAnotherMonitorOwnsThePidfile(t *testing.T) {
	f := &superviseFixture{present: true, scripts: []string{"exit 3", "exit 0"}}
	if err := f.supervise(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.revived) != 1 || len(f.delays) != 0 {
		t.Fatalf("runs %v delays %v", f.revived, f.delays)
	}
}

func TestSuperviseStopsOnceRuntimeIsPurged(t *testing.T) {
	f := &superviseFixture{present: false, scripts: []string{"kill -KILL $$", "exit 0"}}
	if err := f.supervise(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.revived) != 1 || len(f.delays) != 0 {
		t.Fatalf("runs %v delays %v", f.revived, f.delays)
	}
}

func TestSuperviseBacksOffToCapAndResetsAfterStableRun(t *testing.T) {
	scripts := make([]string, 10)
	for i := range scripts {
		scripts[i] = "exit 2"
	}
	scripts = append(scripts, "exit 0")
	runFor := make([]time.Duration, len(scripts))
	runFor[8] = time.Minute
	f := &superviseFixture{present: true, scripts: scripts, runFor: runFor}
	if err := f.supervise(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := time.Second
	want := []time.Duration{s, 2 * s, 4 * s, 8 * s, 16 * s, 32 * s, 60 * s, 60 * s, s, 2 * s}
	if !reflect.DeepEqual(f.delays, want) {
		t.Fatalf("delays %v, want %v", f.delays, want)
	}
}

func TestSuperviseTerminationEndsWithoutRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &superviseFixture{present: true, scripts: []string{"sleep 30", "exit 0"}}
	time.AfterFunc(100*time.Millisecond, cancel)
	if err := f.supervise(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.revived) != 1 || len(f.delays) != 0 {
		t.Fatalf("runs %v delays %v", f.revived, f.delays)
	}
}

func TestRootStartLaunchesSupervisorThatRevivesServe(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("checks the non-root refusal")
	}
	if err := run([]string{"supervise", "--package", "TestApp"}); err == nil || err.Error() != "root required" {
		t.Fatalf("supervise mode: %v", err)
	}
	if err := run([]string{"control", "start", "--package", "TestApp", "--revived"}); err == nil || err.Error() != "unexpected revive" {
		t.Fatalf("revive outside serve: %v", err)
	}
	if got := supervisorCommand("/x/runtime/executor", "TestApp").Args; !reflect.DeepEqual(got, []string{"/x/runtime/executor", "supervise", "--package", "TestApp"}) {
		t.Fatal(got)
	}
	for revived, want := range map[bool][]string{false: {"serve", "--package", "TestApp"}, true: {"serve", "--package", "TestApp", "--revived"}} {
		if got := serveArgs("TestApp", revived); !reflect.DeepEqual(got, want) {
			t.Fatal(got)
		}
	}
	if exitStatus(fmt.Errorf("serve: %w", errAlreadyRunning)) != exitAlreadyRunning || exitStatus(errors.New("other")) != 1 {
		t.Fatal("exit status")
	}
}
