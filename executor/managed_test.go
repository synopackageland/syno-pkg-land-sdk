package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestPauseResumeOnlyApprovedPinsAndPurgeWhilePaused(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same pins", true: "different policy"}[changed], func(t *testing.T) {
			dir := realTestPath(t, t.TempDir())
			os.WriteFile(filepath.Join(dir, "executor"), []byte("approved executable"), 0600)
			os.WriteFile(filepath.Join(dir, "policy.json"), []byte("approved policy"), 0600)
			ed, _ := digestFile(context.Background(), filepath.Join(dir, "executor"), maxExecutorBytes)
			pd, _ := digestFile(context.Background(), filepath.Join(dir, "policy.json"), maxPolicyBytes)
			actions := make(chan string)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var opened, closed, purged atomic.Int32
			socket := filepath.Join(realTestPath(t, t.TempDir()), "s")
			done := make(chan error, 1)
			go func() {
				done <- manageService(ctx, actions, func() (func(), error) {
					listener, err := net.Listen("unix", socket)
					if err != nil {
						return nil, err
					}
					opened.Add(1)
					return func() { listener.Close(); closed.Add(1) }, nil
				}, func(ctx context.Context) bool { return samePackagePins(ctx, dir, ed, pd) }, func() error { purged.Add(1); return nil })
			}()
			await := func(v *atomic.Int32, want int32) {
				t.Helper()
				for v.Load() != want {
					select {
					case <-ctx.Done():
						t.Fatal("lifecycle timeout")
					default:
						time.Sleep(time.Millisecond)
					}
				}
			}
			await(&opened, 1)
			actions <- "stop"
			await(&closed, 1)
			if _, err := os.Stat(socket); !os.IsNotExist(err) {
				t.Fatal("paused socket remains")
			}
			select {
			case err := <-done:
				t.Fatal("stop ended monitor", err)
			default:
			}
			if changed {
				os.WriteFile(filepath.Join(dir, "policy.json"), []byte("new policy"), 0600)
			}
			actions <- "start"
			if changed {
				time.Sleep(15 * time.Millisecond)
				if opened.Load() != 1 {
					t.Fatal("unapproved policy resumed")
				}
			} else {
				await(&opened, 2)
				conn, err := net.Dial("unix", socket)
				if err != nil {
					t.Fatal("approved socket did not resume", err)
				}
				conn.Close()
			}
			actions <- "purge"
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if purged.Load() != 1 || opened.Load() != closed.Load() {
				t.Fatal("purge did not close service and clear state")
			}
		})
	}
}

func TestPausedRequestCannotReachDSMOrWriter(t *testing.T) {
	e, d, w := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := e.Handle(ctx, Request{Version: 1, Op: "delegate.create", Auth: Auth{Cookie: "session", SynoToken: "token"}, Params: json.RawMessage(`{"job_id":"job","ops":["files.write"],"scope":"home","path":"/home","ttl_seconds":60}`)})
	if out.Code != "EXECUTOR_PAUSED" || d.apiCalls != 0 || w.calls != 0 {
		t.Fatal("paused request reached execution", out)
	}
}

func TestRootControlSignalsPauseThenPurgeWithoutMailbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	actions := make(chan string, 1)
	stop := rootControlSignals(ctx, actions)
	defer stop()
	var opened, closed, purged atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- manageService(ctx, actions, func() (func(), error) { opened.Add(1); return func() { closed.Add(1) }, nil }, func(context.Context) bool { return true }, func() error { purged.Add(1); return nil })
	}()
	wait := func(value *atomic.Int32) {
		t.Helper()
		for value.Load() == 0 {
			select {
			case <-ctx.Done():
				t.Fatal("signal timeout")
			default:
				time.Sleep(time.Millisecond)
			}
		}
	}
	wait(&opened)
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	wait(&closed)
	select {
	case <-done:
		t.Fatal("root stop terminated monitor")
	default:
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || purged.Load() != 1 {
		t.Fatal("root purge failed", err)
	}
}

// An anchored install runs inside the pin check; SIGTERM from root control
// start must not end the monitor before that install removes its staged copy.
func TestShutdownWaitsForInFlightPinCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var finished atomic.Bool
	done := make(chan error, 1)
	go func() {
		done <- manageService(ctx, make(chan string), func() (func(), error) { return func() {}, nil }, func(check context.Context) bool {
			close(started)
			<-check.Done()
			time.Sleep(20 * time.Millisecond)
			finished.Store(true)
			return false
		}, func() error { return nil })
	}()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !finished.Load() {
		t.Fatal("monitor ended while the pin check was still running")
	}
}
