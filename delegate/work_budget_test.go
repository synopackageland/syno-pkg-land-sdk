package delegate

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestWorkBudgetStartsAfterAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		seen := make(chan Request, 1)
		c := &Client{Socket: filepath.Join(t.TempDir(), "executor.sock"), WorkBudget: 125 * time.Second}
		var releases []func()
		for range executorRootSlots {
			release, err := acquireAdmission(context.Background(), c.Socket)
			if err != nil {
				t.Fatal(err)
			}
			releases = append(releases, release)
		}
		done := make(chan error, 1)
		go func() { done <- c.Call(context.Background(), "webapi.call", nil, nil, nil) }()
		synctest.Wait()
		time.Sleep(10 * time.Minute)
		listener, err := net.Listen("unix", c.Socket)
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			var r Request
			json.NewDecoder(conn).Decode(&r)
			seen <- r
			conn.Write([]byte("{\"ok\":true,\"result\":{}}\n"))
		}()
		for _, release := range releases {
			release()
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if got := (<-seen).DeadlineMS; got != 115000 {
			t.Fatalf("deadline_ms = %d, want 115000", got)
		}
	})
}

func TestWorkBudgetDoesNotResetAcrossBusyRetries(t *testing.T) {
	var calls atomic.Int32
	c := executor(t, func(Request) string { calls.Add(1); return "{\"ok\":false,\"code\":\"ROOT_BUSY\"}\n" })
	c.Timeout, c.WorkBudget = ExecutorTimeout, 250*time.Millisecond
	start := time.Now()
	err := c.Call(context.Background(), "webapi.call", nil, nil, nil)
	checkNotSent(t, err, true)
	if calls.Load() < 2 || time.Since(start) > time.Second {
		t.Fatal("busy retries reset the work deadline")
	}
}

func TestWorkBudgetExpirationAfterSendIsUncertain(t *testing.T) {
	finished := make(chan struct{})
	defer close(finished)
	c := executor(t, func(Request) string { <-finished; return "" })
	c.Timeout, c.WorkBudget = ExecutorTimeout, 100*time.Millisecond
	err := c.Call(context.Background(), "webapi.call", nil, nil, nil)
	code(t, err, CodeExecutorTimeout)
	checkNotSent(t, err, false)
}

func TestWorkBudgetRetainsExplicitSocketLimitAndLegacyWire(t *testing.T) {
	for _, tc := range []struct {
		budget, timeout time.Duration
		want            int64
	}{
		{125 * time.Second, 65 * time.Second, 60000},
		{0, ExecutorTimeout, 0},
		{-time.Second, ExecutorTimeout, 0},
	} {
		t.Run(tc.budget.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				seen := make(chan Request, 1)
				c := executor(t, func(r Request) string { seen <- r; return "{\"ok\":true,\"result\":{}}\n" })
				c.Timeout, c.WorkBudget = tc.timeout, tc.budget
				if err := c.Call(context.Background(), "webapi.call", nil, nil, nil); err != nil {
					t.Fatal(err)
				}
				if got := (<-seen).DeadlineMS; got != tc.want {
					t.Fatalf("got %d, want %d", got, tc.want)
				}
			})
		})
	}
}

func TestWorkBudgetQueueStillHonorsParentDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &Client{Socket: "queued-executor.sock", WorkBudget: time.Minute}
		for range executorRootSlots {
			release, err := acquireAdmission(context.Background(), c.Socket)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		start := time.Now()
		err := c.Call(ctx, "webapi.call", nil, nil, nil)
		checkNotSent(t, err, true)
		if time.Since(start) != 10*time.Minute {
			t.Fatal("work budget expired in the queue")
		}
	})
}

func TestWorkBudgetHonorsParentAndOperationReserves(t *testing.T) {
	for _, op := range []string{"status", "root.run", "root.start", "webapi.call"} {
		for _, parentBudget := range []time.Duration{0, 65 * time.Second} {
			t.Run(op+parentBudget.String(), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					seen := make(chan Request, 1)
					c := executor(t, func(r Request) string { seen <- r; return "{\"ok\":true,\"result\":{}}\n" })
					c.Timeout, c.WorkBudget = ExecutorTimeout, 125*time.Second
					ctx := context.Background()
					if parentBudget > 0 {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, parentBudget)
						defer cancel()
					}
					if err := c.Call(ctx, op, nil, map[string]string{"response_type": "raw"}, nil); err != nil {
						t.Fatal(err)
					}
					want := int64(115000)
					if parentBudget > 0 {
						want = 55000
					}
					if got := (<-seen).DeadlineMS; got != want {
						t.Fatalf("got %d, want %d", got, want)
					}
				})
			})
		}
	}
}
