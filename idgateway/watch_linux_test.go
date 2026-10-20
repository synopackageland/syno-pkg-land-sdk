package idgateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchFileSeesAReplacementByRename(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "passwd"), []byte("a"), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	changed, done := make(chan struct{}, 8), make(chan error, 1)
	go func() { done <- watchFile(ctx, dir, "passwd", func() { changed <- struct{}{} }) }()
	time.Sleep(100 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "group"), []byte("g"), 0o644)
	select {
	case <-changed:
		t.Fatal("another file reported")
	case <-time.After(500 * time.Millisecond):
	}
	os.WriteFile(filepath.Join(dir, "passwd.tmp"), []byte("b"), 0o644)
	os.Rename(filepath.Join(dir, "passwd.tmp"), filepath.Join(dir, "passwd"))
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("replacement not reported")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not stop")
	}
}

func TestWatchDeletesAtStartupAndOnEachPasswdChange(t *testing.T) {
	f := newDeletions(t)
	f.deleteLocal(t, "bob") // missed while the package was stopped
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); <-done })
	go func() { f.Watch(ctx, t.Logf); close(done) }()
	waitFor(t, func() bool { return f.actionLog() == "1027 bob\n" })
	f.deleteLocal(t, "alice")
	waitFor(t, func() bool { return f.actionLog() == "1027 bob\n1026 alice\n" })
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}

func TestWatchRetriesAPendingDeletionUntilItSucceeds(t *testing.T) {
	saved := pendingRetry
	t.Cleanup(func() { pendingRetry = saved }) // after Watch stops
	pendingRetry = 50 * time.Millisecond
	f := newDeletions(t)
	fail := filepath.Join(filepath.Dir(f.passwd), "fail")
	os.WriteFile(fail, nil, 0o600) // the app is not up yet at startup
	f.deleteLocal(t, "bob")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); <-done })
	go func() { f.Watch(ctx, t.Logf); close(done) }()
	waitFor(t, func() bool { p, _ := f.loadPending(); return len(p) == 1 && p[0].Attempts >= 2 })
	os.Remove(fail)
	waitFor(t, func() bool { return f.actionLog() == "1027 bob\n" })
	// The action writes its log before Watch persists the completed deletion.
	// Observe that commit too, rather than racing the watcher after the log.
	waitFor(t, func() bool { p, err := f.loadPending(); return err == nil && len(p) == 0 })
}
