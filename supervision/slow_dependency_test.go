package supervision

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestDependencyAllowsSlowProbeAndCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			timer := time.AfterFunc(90*time.Second, cancel)
			defer timer.Stop()
			if canceled {
				go func() { time.Sleep(time.Second); cancel() }()
			}
			err := Dependency(ctx, func(ctx context.Context) error {
				calls++
				select {
				case <-time.After(60 * time.Second):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, nil)
			if canceled && err == nil || !canceled && (err != nil || calls != 1) {
				t.Fatalf("calls=%d canceled=%v: %v", calls, canceled, err)
			}
		})
	}
}
