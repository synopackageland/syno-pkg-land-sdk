//go:build unix

package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/background"
)

func workerConfig(t *testing.T, capabilities string) Config {
	t.Helper()
	dir := helperAppDir(t, "env", capabilities, `{"schema":1,"default":"administrators","methods":{}}`)
	return Config{PackageID: "hello", AppDir: dir, DataDir: t.TempDir()}
}

var worker = background.WorkerInfo{PID: os.Getpid(), UID: os.Getuid(), Username: "hello"}

// The worker runs the jobs bg.submit queued, on the same store.
func TestWorkerRunsTheQueuedJobs(t *testing.T) {
	cfg := workerConfig(t, `,"capabilities":{"jobs":true}`)
	alice := background.Submitter{User: "alice", UID: "1001"}
	store := background.NewRequestStore(filepath.Join(cfg.DataDir, "background"))
	job, _, err := store.Submit(context.Background(), background.SubmitRequest{Key: "k1", Name: "hash", Data: []byte("abc"), Submitter: alice})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Worker(ctx, cfg, worker) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("worker: %v", err)
		}
	}()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		got, err := store.GetFor(context.Background(), job.ID, background.Access{Owner: alice})
		if err == nil && got.State == background.StateSucceeded {
			return
		}
	}
	t.Fatal("the job did not run")
}

func TestWorkerNeedsABackgroundCapabilityAndNoRoot(t *testing.T) {
	if err := Worker(context.Background(), workerConfig(t, `,"capabilities":{"portal":true}`), worker); !errors.Is(err, ErrNoBackground) {
		t.Fatalf("no capability: %v", err)
	}
	root := worker
	root.UID = 0
	if err := Worker(context.Background(), workerConfig(t, `,"capabilities":{"jobs":true}`), root); err == nil {
		t.Fatal("ran as root")
	}
	cfg := workerConfig(t, `,"capabilities":{"jobs":true}`)
	cfg.ManifestDigest = "00"
	if err := Worker(context.Background(), cfg, worker); err == nil {
		t.Fatal("ran another manifest")
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "background")); err == nil {
		t.Fatal("a refused worker touched the job store")
	}
}
