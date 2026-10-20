//go:build linux

package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimePeriodicStopsChildrenWithMain(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tick")
	ctx, cancel := context.WithTimeout(context.Background(), 1300*time.Millisecond)
	defer cancel()
	periodic := []PeriodicCommand{{Argv: []string{"/usr/bin/touch", file}, IntervalSeconds: 1, TimeoutSeconds: 1}}
	start := time.Now()
	if err := RunWithPeriodic(ctx, []string{"/bin/sleep", "30"}, periodic); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("children survived cancellation")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("declared periodic task never ran", err)
	}
}
