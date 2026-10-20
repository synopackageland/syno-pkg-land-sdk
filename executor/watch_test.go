package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLifecycleWatch(t *testing.T) {
	for _, action := range []string{"stop", "purge"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			pkg := filepath.Join(root, "package")
			control := filepath.Join(pkg, "var", "executor", "control")
			os.MkdirAll(filepath.Dir(control), 0700)
			os.WriteFile(filepath.Join(pkg, "INFO"), []byte("package=\"TestApp\"\nversion=\"1\"\n"), 0600)
			monitor := newLifecycleMonitor("TestApp", pkg, control, func(string) {})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := make(chan string, 1)
			go func() { result <- monitor.watch(ctx, time.Millisecond) }()
			select {
			case got := <-result:
				t.Fatalf("stopped healthy package: %s", got)
			case <-time.After(10 * time.Millisecond):
			}
			switch action {
			default:
				os.WriteFile(control, []byte(action+"\n"), 0600)
			}
			got := <-result
			want := "purge"
			if action == "stop" {
				want = "stop"
			}
			if got != want || ctx.Err() != nil {
				t.Fatalf("got %q, want %q (%v)", got, want, ctx.Err())
			}
			state := filepath.Join(root, "state")
			socket := filepath.Join(root, "socket")
			os.MkdirAll(filepath.Join(state, "runtime"), 0700)
			os.MkdirAll(filepath.Join(state, "delegations"), 0700)
			os.MkdirAll(socket, 0700)
			if err := finishControl(state, filepath.Join(state, "runtime"), socket, got == "purge"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(socket); !os.IsNotExist(err) {
				t.Fatal("socket survived")
			}
			_, err := os.Stat(state)
			if (got == "purge") != os.IsNotExist(err) {
				t.Fatal("wrong protected state retention", err)
			}
		})
	}
}

// Observe exactly one filesystem poll at an injected time. Cancellation ends a
// poll that has no action, without sleeping or waiting ten real minutes.
func observeLifecycle(t *testing.T, m *lifecycleMonitor, now time.Time) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.now = func() time.Time { cancel(); return now }
	return m.watch(ctx, time.Hour)
}

func TestMissingPackagePurgeRequiresTenContinuousMinutes(t *testing.T) {
	for _, missing := range []string{"directory", "INFO", "identity", "mismatch", "duplicate"} {
		t.Run(missing, func(t *testing.T) {
			pkg := filepath.Join(t.TempDir(), "package")
			if err := os.Mkdir(pkg, 0700); err != nil {
				t.Fatal(err)
			}
			info := filepath.Join(pkg, "INFO")
			content := "package=\"TestApp\"\n"
			switch missing {
			case "directory":
				if err := os.Remove(pkg); err != nil {
					t.Fatal(err)
				}
			case "identity":
				content = "version=\"2\"\n"
			case "mismatch":
				content = "package=\"OtherApp\"\n"
			case "duplicate":
				content += content
			}
			if missing != "directory" && missing != "INFO" {
				if err := os.WriteFile(info, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			m := newLifecycleMonitor("TestApp", pkg, filepath.Join(pkg, "control"), func(string) {})
			start := time.Unix(1000, 0)
			for _, elapsed := range []time.Duration{0, time.Minute, 10*time.Minute - time.Nanosecond} {
				if got := observeLifecycle(t, m, start.Add(elapsed)); got != "stop" {
					t.Fatalf("at %s: %q", elapsed, got)
				}
			}
			if got := observeLifecycle(t, m, start.Add(10*time.Minute)); got != "purge" {
				t.Fatalf("at cutoff: %q", got)
			}
		})
	}
}

func TestMissingPackageTimerResetsWhenIdentityReturns(t *testing.T) {
	pkg := t.TempDir()
	info := filepath.Join(pkg, "INFO")
	m := newLifecycleMonitor("TestApp", pkg, filepath.Join(pkg, "control"), func(string) {})
	start := time.Unix(1000, 0)
	if got := observeLifecycle(t, m, start); got != "stop" {
		t.Fatal(got)
	}
	if err := os.WriteFile(info, []byte("package=\"TestApp\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := observeLifecycle(t, m, start.Add(9*time.Minute)); got != "stop" {
		t.Fatal(got)
	}
	if err := os.Remove(info); err != nil {
		t.Fatal(err)
	}
	if got := observeLifecycle(t, m, start.Add(10*time.Minute)); got != "stop" {
		t.Fatal("timer not reset", got)
	}
	if got := observeLifecycle(t, m, start.Add(19*time.Minute)); got != "stop" {
		t.Fatal("purged too soon", got)
	}
	if got := observeLifecycle(t, m, start.Add(20*time.Minute)); got != "purge" {
		t.Fatal("missing fallback purge", got)
	}
}

func TestPurgeMailboxOverridesMissingPackageGracePeriod(t *testing.T) {
	pkg := filepath.Join(t.TempDir(), "missing")
	control := filepath.Join(t.TempDir(), "control")
	if err := os.WriteFile(control, []byte("purge\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := newLifecycleMonitor("TestApp", pkg, control, func(string) {})
	if got := observeLifecycle(t, m, time.Unix(1000, 0)); got != "purge" {
		t.Fatal("purge mailbox delayed", got)
	}
}

func TestMissingPackageRejectsStartAndUnreadableInfoResetsAbsence(t *testing.T) {
	pkg := t.TempDir()
	info := filepath.Join(pkg, "INFO")
	control := filepath.Join(pkg, "control")
	if err := os.WriteFile(control, []byte("start\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := newLifecycleMonitor("TestApp", pkg, control, func(string) {})
	start := time.Unix(1000, 0)
	if got := observeLifecycle(t, m, start); got != "stop" {
		t.Fatal("start accepted without package identity", got)
	}
	// A symlink cannot be trusted as INFO, but does not prove it is absent.
	if err := os.Symlink(control, info); err != nil {
		t.Fatal(err)
	}
	if got := observeLifecycle(t, m, start.Add(9*time.Minute)); got != "stop" {
		t.Fatal("unreadable identity resumed or purged", got)
	}
	if err := os.Remove(info); err != nil {
		t.Fatal(err)
	}
	if got := observeLifecycle(t, m, start.Add(10*time.Minute)); got != "stop" {
		t.Fatal("unverified absence counted toward purge", got)
	}
	if got := observeLifecycle(t, m, start.Add(20*time.Minute)); got != "purge" {
		t.Fatal("confirmed absence did not purge", got)
	}
}

func TestReappearedPackageHonorsStopCapturedBeforeApprovedBoot(t *testing.T) {
	pkg := t.TempDir()
	info := filepath.Join(pkg, "INFO")
	control := filepath.Join(pkg, "control")
	if err := os.WriteFile(control, []byte("stop\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := newLifecycleMonitor("TestApp", pkg, control, func(string) {})
	start := time.Unix(1000, 0)
	if got := observeLifecycle(t, m, start); got != "stop" {
		t.Fatal(got)
	}
	if err := os.WriteFile(info, []byte("package=\"TestApp\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return start.Add(time.Minute) }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if got := m.watch(ctx, time.Millisecond); got != "stop" || ctx.Err() != nil {
		t.Fatal("old stop ignored after absence", got, ctx.Err())
	}
}
func TestLifecycleWatchIgnoresInvalidControlAndResumesAfterOldStop(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "package")
	control := filepath.Join(pkg, "var", "executor", "control")
	os.MkdirAll(filepath.Dir(control), 0700)
	os.WriteFile(filepath.Join(pkg, "INFO"), []byte("package=\"TestApp\"\n"), 0600)
	os.WriteFile(control, []byte("stop\n"), 0600)
	var mu sync.Mutex
	logs := 0
	observed := make(chan struct{}, 32)
	monitor := newLifecycleMonitor("TestApp", pkg, control, func(string) { mu.Lock(); logs++; mu.Unlock(); observed <- struct{}{} })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan string, 1)
	go func() { result <- monitor.watch(ctx, time.Millisecond) }()
	checkRunning := func() {
		t.Helper()
		select {
		case got := <-result:
			t.Fatalf("unexpected action %q", got)
		case <-time.After(12 * time.Millisecond):
		}
	}
	checkRunning()
	for _, value := range []string{"STOP", " purge", "purge\n\n", "purge;id", "wrong"} {
		os.WriteFile(control, []byte(value), 0600)
		select {
		case <-observed:
		case got := <-result:
			t.Fatalf("unexpected action %q", got)
		case <-ctx.Done():
			t.Fatal("invalid request not observed")
		}
	}
	mu.Lock()
	count := logs
	mu.Unlock()
	if count < 5 {
		t.Fatalf("missing invalid-control logs: %d", count)
	}
	// Atomic replacement is a fresh request, even when its contents match old stop.
	next := control + ".new"
	os.WriteFile(next, []byte("stop\n"), 0600)
	os.Rename(next, control)
	if got := <-result; got != "stop" || ctx.Err() != nil {
		t.Fatal(got, ctx.Err())
	}
}
func TestLifecycleWatchHonorsPendingPurgeAtStartup(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "INFO"), []byte("package=\"TestApp\"\n"), 0600)
	control := filepath.Join(root, "control")
	os.WriteFile(control, []byte("purge\n"), 0600)
	monitor := newLifecycleMonitor("TestApp", root, control, func(string) {})
	if got := monitor.watch(context.Background(), time.Millisecond); got != "purge" {
		t.Fatal(got)
	}
}

func TestLifecycleMonitorReadsStartAfterStopAndKeepsCheckingPurge(t *testing.T) {
	dir := realTestPath(t, t.TempDir())
	os.WriteFile(filepath.Join(dir, "INFO"), []byte("package=\"TestApp\"\n"), 0600)
	control := filepath.Join(dir, "control")
	monitor := newLifecycleMonitor("TestApp", dir, control, func(string) {})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, action := range []string{"stop", "start", "stop", "purge"} {
		tmp := control + ".new"
		os.WriteFile(tmp, []byte(action+"\n"), 0600)
		os.Rename(tmp, control)
		if got := monitor.watch(ctx, time.Millisecond); got != action || ctx.Err() != nil {
			t.Fatal(got, action, ctx.Err())
		}
	}
}

// A supervisor restart is not a new administrator approval: the stop already in
// the mailbox keeps the revived monitor paused.
func TestRevivedMonitorHonorsStopAlreadyInMailbox(t *testing.T) {
	pkg := t.TempDir()
	os.WriteFile(filepath.Join(pkg, "INFO"), []byte("package=\"TestApp\"\n"), 0600)
	control := filepath.Join(pkg, "control")
	os.WriteFile(control, []byte("stop\n"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if got := newRevivedLifecycleMonitor("TestApp", pkg, control, func(string) {}).watch(ctx, time.Millisecond); got != "stop" || ctx.Err() != nil {
		t.Fatal("revived monitor ignored stop", got, ctx.Err())
	}
}
