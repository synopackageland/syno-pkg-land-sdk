package delegate

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/background"
)

func TestIdleWorkerUsesOneHeartbeatAndClearsOnStop(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("package worker refuses root")
	}
	s := service(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, background.WorkerInfo{PID: os.Getpid(), UID: os.Getuid()}) }()
	stopped := false
	defer func() {
		if stopped {
			return
		}
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	var first time.Time
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := s.Store.Status(ctx)
		if st.Worker != nil {
			first = st.Worker.At
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if first.IsZero() {
		t.Fatal("initial liveness missing")
	}
	dir := filepath.Join(s.StagingDir, "..", "background")
	snapshot := func() map[string]time.Time {
		out := map[string]time.Time{}
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			out[path] = info.ModTime()
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	// Cross several 500ms polling ticks, then the single five-second heartbeat.
	time.Sleep(1600 * time.Millisecond)
	st, err := s.Store.Status(ctx)
	if err != nil || st.Worker == nil || !st.Worker.At.Equal(first) {
		t.Fatalf("poll loop published extra heartbeat: %+v %v", st, err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, _ = s.Store.Status(ctx)
		if st.Worker != nil && st.Worker.At.After(first) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Worker == nil || !st.Worker.At.After(first) {
		t.Fatal("heartbeat never advanced")
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("idle worker mutated persistent spool: %v %v", before, after)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	stopped = true
	st, err = s.Store.Status(context.Background())
	if err != nil || st.Worker != nil {
		t.Fatalf("stopped observation retained: %+v %v", st, err)
	}

}
