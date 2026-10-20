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
