package background

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var admin = Submitter{User: "admin", UID: "1024"}

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "background")
	s := NewStore(dir)
	t.Cleanup(func() {
		if root, err := s.heartbeatRoot(false); err == nil {
			name := root.Name()
			root.Close()
			_ = os.RemoveAll(name)
		}
	})
	return s, dir
}

func submit(t *testing.T, s *Store, key string, data []byte) Job {
	t.Helper()
	job, _, err := s.Submit(context.Background(), SubmitRequest{Key: key, Name: "a.txt", Data: data, Submitter: admin})
	if err != nil {
		t.Fatalf("submit %q: %v", key, err)
	}
	return job
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestSubmitWritesDurableQueuedJobBeforeReturningID(t *testing.T) {
	s, dir := newStore(t)
	data := []byte("hello")
	job := submit(t, s, "key-1", data)
	if job.State != StateQueued || len(job.ID) != 32 || job.InputDigest != digest(data) || job.InputBytes != 5 {
		t.Fatalf("unexpected job %+v", job)
	}
	// A second process (fresh Store on the same directory) sees the job.
	got, err := NewStore(dir).Get(context.Background(), job.ID)
	if err != nil || got.State != StateQueued || got.Submitter != admin || got.Worker != WorkerSHA256V1 {
		t.Fatalf("reopen: %+v %v", got, err)
	}
}

func TestSpoolPermissionsArePrivate(t *testing.T) {
	s, dir := newStore(t)
	job := submit(t, s, "key-1", []byte("x"))
	check := func(path string, want os.FileMode) {
		t.Helper()
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s: mode %v err %v, want %v", path, info.Mode().Perm(), err, want)
		}
	}
	check(dir, 0o700)
	check(filepath.Join(dir, "inputs"), 0o700)
	check(filepath.Join(dir, "inputs", job.ID), 0o600)
	check(filepath.Join(dir, "journal.json"), 0o600)
	check(filepath.Join(dir, "lock"), 0o600)
}

func TestSpoolRefusesWidePermissionsAndSymlinks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "background")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir)
	if _, err := s.Status(context.Background()); err == nil {
		t.Fatal("a world-readable spool must be refused")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(link).Status(context.Background()); err == nil {
		t.Fatal("a symlinked spool must be refused")
	}
}

func TestSubmitIdempotencyKey(t *testing.T) {
	s, _ := newStore(t)
	first := submit(t, s, "same-key", []byte("one"))
	again, dup, err := s.Submit(context.Background(), SubmitRequest{Key: "same-key", Name: "a.txt", Data: []byte("one"), Submitter: admin})
	if err != nil || !dup || again.ID != first.ID {
		t.Fatalf("duplicate: %+v dup=%v err=%v", again, dup, err)
	}
	_, _, err = s.Submit(context.Background(), SubmitRequest{Key: "same-key", Name: "a.txt", Data: []byte("two"), Submitter: admin})
	if !errors.Is(err, ErrKeyConflict) {
		t.Fatalf("different input under the same key: %v", err)
	}
	list, _ := s.List(context.Background())
	if len(list) != 1 {
		t.Fatalf("same key must not queue twice: %d jobs", len(list))
	}
}

func TestSubmitLimits(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if _, _, err := s.Submit(ctx, SubmitRequest{Key: "big", Name: "a", Data: make([]byte, MaxInputBytes+1), Submitter: admin}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize: %v", err)
	}
	for _, bad := range []SubmitRequest{
		{Key: "", Name: "a", Data: []byte("x")},
		{Key: "has space", Name: "a", Data: []byte("x")},
		{Key: strings.Repeat("k", 65), Name: "a", Data: []byte("x")},
		{Key: "ok", Name: "a", Data: nil},
		{Key: "ok", Name: "bad\nname", Data: []byte("x")},
		{Key: "ok", Name: "", Data: []byte("x")},
	} {
		bad.Submitter = admin
		if _, _, err := s.Submit(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: want ErrInvalid, got %v", bad, err)
		}
	}
	for i := 0; i < MaxQueued; i++ {
		_, _, err := s.Submit(ctx, SubmitRequest{Key: fmt.Sprint(i), Name: "a", Data: []byte("x"), Submitter: Submitter{User: "admin", UID: fmt.Sprint(2000 + i)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Submit(ctx, SubmitRequest{Key: "one-more", Name: "a", Data: []byte("x"), Submitter: admin}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue full: %v", err)
	}
}

func TestSubmitTotalQuota(t *testing.T) {
	s, _ := newStore(t)
	s.limits.MaxTotalBytes = 10 + resultReserve
	submit(t, s, "k1", []byte("123456"))
	if _, _, err := s.Submit(context.Background(), SubmitRequest{Key: "k2", Name: "a", Data: []byte("123456"), Submitter: admin}); !errors.Is(err, ErrQuota) {
		t.Fatalf("quota: %v", err)
	}
}

func TestClaimRunsOneJobAtATimeInSubmissionOrder(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	a := submit(t, s, "ka", []byte("a"))
	b := submit(t, s, "kb", []byte("b"))
	claimed, err := s.Claim(ctx)
	if err != nil || claimed == nil || claimed.ID != a.ID || claimed.State != StateRunning {
		t.Fatalf("first claim: %+v %v", claimed, err)
	}
	if again, err := s.Claim(ctx); err != nil || again != nil {
		t.Fatalf("a second job must wait while one runs: %+v %v", again, err)
	}
	if err := s.Finish(ctx, a.ID, Outcome{State: StateSucceeded, Result: &Result{SHA256: digest([]byte("a")), Bytes: 1}}); err != nil {
		t.Fatal(err)
	}
	next, err := s.Claim(ctx)
	if err != nil || next == nil || next.ID != b.ID {
		t.Fatalf("next claim: %+v %v", next, err)
	}
}

func TestFinishRecordsResultAndRetainsPrivateCopy(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("abc"))
	if _, err := s.Claim(ctx); err != nil {
		t.Fatal(err)
	}
	want := Result{SHA256: digest([]byte("abc")), Bytes: 3}
	if err := s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &want}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, job.ID)
	if got.State != StateSucceeded || got.Result == nil || !reflect.DeepEqual(*got.Result, want) || got.FinishedAt.IsZero() {
		t.Fatalf("finished job: %+v", got)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "inputs", job.ID)); err != nil || string(b) != "abc" {
		t.Fatalf("completed private copy must remain available for schedules: %v", err)
	}
	if err := s.Finish(ctx, job.ID, Outcome{State: StateFailed, ErrorCode: "x"}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("finishing twice: %v", err)
	}
}

func TestCancelQueuedJobIsImmediate(t *testing.T) {
	s, dir := newStore(t)
	job := submit(t, s, "k", []byte("x"))
	got, err := s.Cancel(context.Background(), job.ID)
	if err != nil || got.State != StateCanceled {
		t.Fatalf("cancel: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "inputs", job.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled input must be removed: %v", err)
	}
	if c, _ := s.Claim(context.Background()); c != nil {
		t.Fatal("a canceled job must never run")
	}
}

func TestCancelRunningJobRequestsStopAndBeatsLaterCompletion(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	if _, err := s.Claim(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.Cancel(ctx, job.ID)
	if err != nil || got.State != StateRunning || !got.CancelRequested {
		t.Fatalf("cancel running: %+v %v", got, err)
	}
	if err := s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{Bytes: 1}}); err != nil {
		t.Fatal(err)
	}
	final, _ := s.Get(ctx, job.ID)
	if final.State != StateCanceled || final.Result != nil {
		t.Fatalf("cancel requested first must win: %+v", final)
	}
}

func TestCancelAfterCompletionIsRefused(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	_, _ = s.Claim(ctx)
	_ = s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{Bytes: 1}})
	if _, err := s.Cancel(ctx, job.ID); !errors.Is(err, ErrNotCancelable) {
		t.Fatalf("late cancel: %v", err)
	}
	if got, _ := s.Get(ctx, job.ID); got.State != StateSucceeded {
		t.Fatalf("completion must stand: %+v", got)
	}
}

func TestUnknownJobIsNotFound(t *testing.T) {
	s, _ := newStore(t)
	for _, id := range []string{"nope", strings.Repeat("0", 32), "../journal.json"} {
		if _, err := s.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%q: %v", id, err)
		}
		if _, err := s.Cancel(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cancel %q: %v", id, err)
		}
	}
}

func TestRecoverMarksRunningInterruptedWithoutReplay(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	running := submit(t, s, "k1", []byte("1"))
	queued := submit(t, s, "k2", []byte("2"))
	_, _ = s.Claim(ctx)
	// The worker was killed: a new process recovers the journal.
	fresh := NewStore(dir)
	if err := fresh.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := fresh.Get(ctx, running.ID); got.State != StateInterrupted || got.ErrorCode != "worker-stopped" {
		t.Fatalf("running: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "inputs", running.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted input must be removed: %v", err)
	}
	if got, _ := fresh.Get(ctx, queued.ID); got.State != StateQueued {
		t.Fatalf("queued jobs of the same worker revision stay queued: %+v", got)
	}
}

func TestRecoverMarksCancelRequestedRunningAsCanceled(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	_, _ = s.Claim(ctx)
	_, _ = s.Cancel(ctx, job.ID)
	if err := NewStore(dir).Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, job.ID); got.State != StateCanceled {
		t.Fatalf("%+v", got)
	}
}

func TestRecoverInterruptsQueuedJobsFromAnotherWorkerRevision(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	newer := NewStore(dir)
	newer.revision = WorkerRevision + 1
	if err := newer.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, job.ID)
	if got.State != StateInterrupted || got.ErrorCode != "worker-changed" {
		t.Fatalf("an upgrade must not silently run old queued work: %+v", got)
	}
}

func TestRecoverRemovesOrphanInputs(t *testing.T) {
	s, dir := newStore(t)
	submit(t, s, "k", []byte("x"))
	orphan := filepath.Join(dir, "inputs", strings.Repeat("a", 32))
	if err := os.WriteFile(orphan, []byte("left by a crash"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan input must be removed: %v", err)
	}
}

func TestOpenInputReturnsStoredBytesOnlyForRunningJob(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("payload"))
	if _, err := s.OpenInput(job.ID); err == nil {
		t.Fatal("a queued job's input is not handed to a worker")
	}
	_, _ = s.Claim(ctx)
	f, err := s.OpenInput(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(f)
	if buf.String() != "payload" {
		t.Fatalf("input: %q", buf.String())
	}
}

func TestHistoryIsBounded(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	for i := 0; i < MaxHistory+5; i++ {
		job := submit(t, s, "key-"+time.Duration(i).String(), []byte("x"))
		_, _ = s.Claim(ctx)
		_ = s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{Bytes: 1}})
	}
	list, _ := s.List(ctx)
	if len(list) != MaxHistory {
		t.Fatalf("history: %d jobs", len(list))
	}
}

func TestStatusReportsWorkerHeartbeat(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	st, _ := s.Status(ctx)
	if st.Worker != nil || st.Revision != WorkerRevision || st.Algorithm != WorkerSHA256V1 {
		t.Fatalf("status: %+v", st)
	}
	if err := s.Heartbeat(ctx, WorkerInfo{PID: os.Getpid(), UID: os.Getuid(), Username: "SynologyApp"}); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Status(ctx)
	if st.Worker == nil || st.Worker.UID != os.Getuid() || st.Worker.PID != os.Getpid() || st.Worker.At.IsZero() {
		t.Fatalf("worker: %+v", st.Worker)
	}
}

func TestRemoveDropsAFinishedJobAndItsRetainedCopy(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	owner := Submitter{User: "fixtureuser1", UID: "1025"}
	job, _, err := s.Submit(ctx, SubmitRequest{Key: "k", Name: "a.txt", Data: []byte("x"), Submitter: owner})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFor(ctx, job.ID, Access{Owner: owner}); !errors.Is(err, ErrNotFinished) {
		t.Fatalf("queued job removed: %v", err)
	}
	_, _ = s.Claim(ctx)
	_ = s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{Bytes: 1}})
	input := filepath.Join(dir, inputs, job.ID)
	if _, err := os.Stat(input); err != nil {
		t.Fatalf("a succeeded job keeps its copy: %v", err)
	}
	if err := s.RemoveFor(ctx, job.ID, Access{Owner: Submitter{User: "other", UID: "1026"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another account removed the job: %v", err)
	}
	if err := s.RemoveFor(ctx, job.ID, Access{Owner: owner}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed job still listed: %v", err)
	}
	if _, err := os.Stat(input); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retained copy left behind: %v", err)
	}
}
