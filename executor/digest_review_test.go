package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewLargePackageHashDoesNotBlockShutdown(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "executor")
	f, err := os.Create(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(1 << 40); err != nil {
		f.Close()
		t.Skip(err)
	}
	f.Close()
	os.WriteFile(filepath.Join(dir, "policy.json"), []byte("policy"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- manageService(ctx, make(chan string), func() (func(), error) { return func() {}, nil }, func(ctx context.Context) bool {
			close(entered)
			return samePackagePins(ctx, dir, strings.Repeat("a", 64), strings.Repeat("b", 64))
		}, func() error { return nil })
	}()
	<-entered
	time.Sleep(20 * time.Millisecond)
	cancel()
	blocked := false
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		blocked = true
	}
	os.Truncate(exe, 0)
	if blocked {
		<-done
		t.Fatal("canceled monitor remains blocked hashing package-controlled file")
	}
}

func TestDigestBoundsAndCancellation(t *testing.T) {
	dir := realTestPath(t, t.TempDir())
	for _, tc := range []struct {
		name  string
		limit int64
	}{{"executor", maxExecutorBytes}, {"policy.json", maxPolicyBytes}} {
		path := filepath.Join(dir, tc.name)
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Truncate(tc.limit + 1); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if _, err = digestFile(context.Background(), path, tc.limit); err == nil {
			t.Fatal("oversize file accepted")
		}
		os.WriteFile(path, []byte("valid"), 0600)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err = digestFile(ctx, path, tc.limit); err != context.Canceled {
			t.Fatal("ignored cancellation", err)
		}
	}
	if _, err := digestFile(context.Background(), dir, maxExecutorBytes); err == nil {
		t.Fatal("directory accepted")
	}
	os.Symlink(filepath.Join(dir, "executor"), filepath.Join(dir, "link"))
	if _, err := digestFile(context.Background(), filepath.Join(dir, "link"), maxExecutorBytes); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestLifecycleCancelsPendingPinCheck(t *testing.T) {
	for _, action := range []string{"stop", "purge", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, canceled := make(chan struct{}), make(chan struct{})
			actions := make(chan string)
			done := make(chan error, 1)
			go func() {
				done <- manageService(ctx, actions, func() (func(), error) { t.Error("opened during canceled check"); return func() {}, nil }, func(ctx context.Context) bool { close(entered); <-ctx.Done(); close(canceled); return false }, func() error { return nil })
			}()
			<-entered
			if action == "shutdown" {
				cancel()
			} else {
				actions <- action
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("pin check not canceled")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("monitor stuck")
			}
		})
	}
}
