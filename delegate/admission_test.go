package delegate

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientRetriesRootBusyUntilAccepted(t *testing.T) {
	for _, op := range []string{"root.run", "root.start", "webapi.call"} {
		t.Run(op, func(t *testing.T) {
			var calls atomic.Int32
			c := executor(t, func(Request) string {
				if calls.Add(1) <= 3 {
					return "{\"ok\":false,\"code\":\"ROOT_BUSY\"}\n"
				}
				return "{\"ok\":true,\"result\":{}}\n"
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Call(ctx, op, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 4 {
				t.Fatal(calls.Load())
			}
		})
	}
}

func TestClientNeverReplaysOtherOutcomes(t *testing.T) {
	for _, reply := range []string{"", "{\"ok\":true}", "{\"ok\":false,\"code\":\"ROOT_TIMEOUT\"}\n", "{\"ok\":false,\"code\":\"WEBAPI_UNCERTAIN\"}\n", "{\"ok\":false,\"code\":\"INTERNAL\"}\n", "{\"ok\":false,\"code\":\"DSM_FAILED\"}\n", "{\"ok\":false,\"code\":\"WRITE_FAILED\"}\n", "{\"ok\":false,\"code\":\"ROOT_STAGING_FULL\"}\n"} {
		t.Run(fmt.Sprintf("reply-%q", reply), func(t *testing.T) {
			var calls atomic.Int32
			c := executor(t, func(Request) string { calls.Add(1); return reply })
			if c.Call(context.Background(), "webapi.call", nil, nil, nil) == nil {
				t.Fatal("expected failure")
			}
			if calls.Load() != 1 {
				t.Fatal("replayed", calls.Load())
			}
		})
	}
}

func TestClientRootBusyWaitHonorsCallerCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			seen := make(chan struct{}, 1)
			c := executor(t, func(Request) string {
				select {
				case seen <- struct{}{}:
				default:
				}
				return "{\"ok\":false,\"code\":\"ROOT_BUSY\"}\n"
			})
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
			}
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.Call(ctx, "webapi.call", nil, nil, nil) }()
			<-seen
			if !deadline {
				cancel()
			}
			select {
			case err := <-done:
				code(t, err, "canceled")
			case <-time.After(time.Second):
				t.Fatal("busy wait ignored caller")
			}
		})
	}
}

func TestClientAdmissionSharedAcrossClientsAndControlBypassesQueue(t *testing.T) {
	entered := make(chan string, 8)
	release := make(chan struct{})
	defer close(release)
	c := executor(t, func(r Request) string {
		entered <- r.Op
		if r.Op == "webapi.call" || r.Op == "root.run" {
			<-release
		}
		return "{\"ok\":true,\"result\":{}}\n"
	})
	c.Timeout = 5 * time.Second
	other := &Client{Socket: c.Socket, Timeout: c.Timeout}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 3)
	go func() { done <- c.Call(ctx, "webapi.call", nil, nil, nil) }()
	go func() { done <- other.Call(ctx, "root.run", nil, nil, nil) }()
	<-entered
	<-entered
	go func() { done <- (&Client{Socket: c.Socket}).Call(ctx, "webapi.call", nil, nil, nil) }()
	select {
	case op := <-entered:
		t.Fatal("third call dispatched", op)
	case <-time.After(50 * time.Millisecond):
	}
	if err := other.Call(ctx, "root.jobs.stop", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if op := <-entered; op != "root.jobs.stop" {
		t.Fatal(op)
	}
	cancel()
	for range 3 {
		select {
		case err := <-done:
			code(t, err, "canceled")
		case <-time.After(time.Second):
			t.Fatal("queue cancellation stuck")
		}
	}
	select {
	case op := <-entered:
		t.Fatal("canceled waiter dispatched", op)
	default:
	}
}

func TestClientAdmissionDoesNotBlockAnotherExecutor(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)
	c := executor(t, func(Request) string { entered <- struct{}{}; <-release; return "" })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	for range 2 {
		go func() { done <- c.Call(ctx, "webapi.call", nil, nil, nil) }()
	}
	<-entered
	<-entered
	other := executor(t, func(Request) string { return "{\"ok\":true,\"result\":{}}\n" })
	if err := other.Call(ctx, "webapi.call", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	<-done
}

func TestAdmissionReleasesIdleSocketEntries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release, err := acquireAdmission(ctx, "unused-executor.sock")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	release()
	admissions.Lock()
	defer admissions.Unlock()
	if _, ok := admissions.sockets["unused-executor.sock"]; ok {
		t.Fatal("idle socket retained")
	}
}
