package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCommandTimeoutIsPerCallAndBounded(t *testing.T) {
	ctx := context.Background()
	for given, want := range map[time.Duration]time.Duration{
		0:                     defaultCommandTimeout,
		-time.Second:          defaultCommandTimeout,
		90 * time.Second:      90 * time.Second,
		time.Hour:             maxCommandTimeout,
		maxCommandTimeout + 1: maxCommandTimeout,
	} {
		if got := commandTimeout(withCommandTimeout(ctx, given)); got != want {
			t.Errorf("%v: %v", given, got)
		}
	}
	if commandTimeout(ctx) != defaultCommandTimeout || defaultCommandTimeout != 20*time.Second {
		t.Fatal(commandTimeout(ctx))
	}
}

func TestExecStopsAtThePerCallTimeout(t *testing.T) {
	started := time.Now()
	_, err := processCommand{}.Exec(withCommandTimeout(context.Background(), 200*time.Millisecond), "/bin/sleep", []string{"5"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
		t.Fatal(err, time.Since(started))
	}
}

func TestSchedulerReadsCannotInitializeTheLiveDatabase(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		isolated bool
	}{
		{[]string{"--exec", "api=SYNO.Core.EventScheduler", "method=get", "version=1", `task_name="TestApp executor boot"`}, true},
		{[]string{"--exec", "api=SYNO.Core.TaskScheduler", "method=list", "version=3", "start=0", "limit=500"}, true},
		{[]string{"--exec", "api=SYNO.Core.TaskScheduler", "method=get", "version=4", "id=7", `real_owner="root"`}, false},
		{[]string{"--exec", "api=SYNO.Core.EventScheduler", "method=delete", "version=1", `task_name="TestApp executor boot"`}, false},
		{[]string{"--exec", "api=SYNO.Core.Share", "method=get", "version=1", `name="reports"`}, false},
	} {
		got := schedulerReadCommand(tc.args)
		if got != tc.isolated {
			t.Fatalf("scheduler isolation %v: %v", tc.args, got)
		}
	}
}

func TestSchedulerSnapshotHelperRejectsAnythingExceptFixedReads(t *testing.T) {
	for _, args := range [][]string{
		{"--exec", "api=SYNO.Core.EventScheduler", "method=delete", "version=1", `task_name="TestApp executor boot"`},
		{"--exec", "api=SYNO.Core.TaskScheduler", "method=list", "version=3", "start=0", "limit=500", "outfile=/etc/passwd"},
		{"--exec-fastwebapi", "api=SYNO.Core.TaskScheduler", "method=list", "version=3", "start=0", "limit=500"},
		{"--exec", "api=SYNO.Core.TaskScheduler", "method=list", "version=3", "start=0", "limit=500", "runner=other"},
		{"--exec", "api=SYNO.Core.EventScheduler", "method=get", "version=1", `task_name="foreign"`},
	} {
		if validSchedulerRead(args) {
			t.Fatalf("unsafe snapshot invocation accepted: %v", args)
		}
	}
}
