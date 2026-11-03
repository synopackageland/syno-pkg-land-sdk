package main

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestSlowDefaultRootProgramAndCommandStillCancel(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				timer := time.AfterFunc(time.Second, cancel)
				defer timer.Stop()
			}
			commandCtx, stop := context.WithTimeout(ctx, commandTimeout(ctx))
			defer stop()
			runner := &fakeRoot{run: func(ctx context.Context, _ rootCall) (int, error) {
				select {
				case <-time.After(60 * time.Second):
					return 0, nil
				case <-ctx.Done():
					return -1, ctx.Err()
				}
			}}
			_, err := runRoot(commandCtx, runner, rootCall{}, (&RootRun{}).timeout(), nil)
			if canceled {
				if err == nil || !errors.Is(commandCtx.Err(), context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			} else if err != nil {
				t.Fatalf("slow command/root program: %v", err)
			}
		})
	}
}
