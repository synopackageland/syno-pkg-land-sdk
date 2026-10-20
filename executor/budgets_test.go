package main

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/budget"
	"testing"
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
