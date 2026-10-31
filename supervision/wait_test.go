package supervision

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestFailedHasNoTimerAndTriggerRearms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := Defaults()
		cfg.Burst = 2
		p := New(cfg, nil)
		ctx := context.Background()
		for i := 0; i < 2; i++ {
			p.Started(time.Now())
			s := p.Exited(time.Now(), Exit{Code: 1})
			if s.State == Backoff {
				if err := Wait(ctx, time.Until(s.NextRetry)); err != nil {
					t.Fatal(err)
				}
			}
		}
		before := p.Snapshot()
		time.Sleep(48 * time.Hour)
		after := p.Snapshot()
		if after.State != Failed || before.Attempts != after.Attempts || !after.NextRetry.IsZero() {
			t.Fatal(after)
		}
		p.Trigger()
		p.Started(time.Now())
		if s := p.Exited(time.Now(), Exit{Code: 1}); s.State != Backoff {
			t.Fatal(s)
		}
	})
}
func TestDependencyDoesNotTimeoutOrCountFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p := New(Defaults(), nil)
		var calls atomic.Int32
		done := make(chan error, 1)
		go func() {
			done <- Dependency(ctx, func(context.Context) error {
				if calls.Add(1) < 1000 {
					return errors.New("offline")
				}
				return nil
			}, func() { p.Waiting("network") })
		}()
		time.Sleep(20 * time.Minute)
		synctest.Wait()
		if p.Snapshot().State != Waiting || p.Snapshot().Attempts != 0 {
			t.Fatal(p.Snapshot())
		}
		cancel()
		synctest.Wait()
		if !errors.Is(<-done, context.Canceled) {
			t.Fatal("not canceled")
		}
	})
}
func TestConcurrencyCapAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLimiter(2)
		ctx, cancel := context.WithCancel(context.Background())
		a, _ := l.Acquire(ctx)
		b, _ := l.Acquire(ctx)
		done := make(chan error, 1)
		go func() {
			release, err := l.Acquire(ctx)
			if release != nil {
				release()
			}
			done <- err
		}()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("limit exceeded")
		default:
		}
		cancel()
		synctest.Wait()
		if !errors.Is(<-done, context.Canceled) {
			t.Fatal("not canceled")
		}
		a()
		b()
	})
}

func TestDependencyAnnouncesBeforeAStalledFirstProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		waiting := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- Dependency(ctx, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func() { close(waiting) })
		}()
		synctest.Wait()
		select {
		case <-waiting:
		default:
			t.Fatal("first probe hid the waiting state behind its timeout")
		}
		cancel()
		synctest.Wait()
		if !errors.Is(<-done, context.Canceled) {
			t.Fatal("stalled probe outlived stop")
		}
	})
}
