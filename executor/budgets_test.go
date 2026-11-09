package main

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/budget"
	"testing"
	"testing/synctest"
	"time"
)

func TestCommandUsesRemainingOuterDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if got := commandTimeout(ctx); got <= 0 || got >= 250*time.Millisecond {
		t.Fatalf("inner command outlives request: %v", got)
	}
}

func TestPolicyPinsLifecycleBudgetsAndRejectsExcess(t *testing.T) {
	p := portalPolicy("Demo")
	limits := budget.Default()
	limits.Stop = 600
	p.Timeouts = &limits
	raw, _ := json.Marshal(p)
	if _, err := parsePolicy(raw, "Demo"); err != nil {
		t.Fatal(err)
	}
	limits.Stop = 3601
	raw, _ = json.Marshal(p)
	if _, err := parsePolicy(raw, "Demo"); err == nil {
		t.Fatal("unbounded policy accepted")
	}
}

func TestCommandReplyReserveIsASeparateHop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// The App's 125 seconds became 120 at the client and 115 on the wire.
		ctx, cancel := context.WithTimeout(context.Background(), 115*time.Second)
		defer cancel()
		for name, got := range map[string]time.Duration{
			"command": commandTimeout(ctx), "stream": streamCommandTimeout(ctx),
		} {
			if got != 110*time.Second {
				t.Fatalf("%s = %v, want 110s", name, got)
			}
		}
	})
}
