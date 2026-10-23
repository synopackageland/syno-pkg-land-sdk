package background

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastRunner(s *Store, work Work) *Runner {
	return &Runner{Store: s, Work: work, Timeout: 2 * time.Second, Poll: 5 * time.Millisecond, CancelPoll: 5 * time.Millisecond,
		Beat: 10 * time.Millisecond, Info: WorkerInfo{PID: os.Getpid(), UID: os.Getuid(), Username: "pkg"}}
}

func start(t *testing.T, r *Runner) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	return func() error { cancel(); return <-done }
}

func waitState(t *testing.T, s *Store, id string, want State) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := s.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == want {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := s.Get(context.Background(), id)
	t.Fatalf("job %s never reached %s: %+v", id, want, job)
	return job
}

func TestSHA256V1WorkHashesInput(t *testing.T) {
	res, err := HashInput(context.Background(), strings.NewReader("abc"))
	if err != nil || res.Bytes != 3 || res.SHA256 != digest([]byte("abc")) {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestSHA256V1WorkStopsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := HashInput(ctx, strings.NewReader(strings.Repeat("x", 1<<20))); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

func TestRefuseRoot(t *testing.T) {
	if RefuseRoot(0) == nil || RefuseRoot(1000) != nil {
		t.Fatal("the worker must refuse to run as root")
	}
}

func TestRunnerProcessesQueuedJobEndToEnd(t *testing.T) {
	s, _ := newStore(t)
	job := submit(t, s, "k", []byte("hello"))
	stop := start(t, fastRunner(s, SHA256V1))
	done := waitState(t, s, job.ID, StateSucceeded)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if done.Result == nil || done.Result.SHA256 != digest([]byte("hello")) || done.Result.Bytes != 5 {
		t.Fatalf("%+v", done)
	}
	st, _ := s.Status(context.Background())
	if st.Worker != nil {
		t.Fatalf("a stopped worker must invalidate its observation: %+v", st.Worker)
	}
}

func TestRunnerFailsJobWhoseInputChanged(t *testing.T) {
	s, dir := newStore(t)
	job := submit(t, s, "k", []byte("hello"))
	if err := os.WriteFile(dir+"/inputs/"+job.ID, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := start(t, fastRunner(s, SHA256V1))
	got := waitState(t, s, job.ID, StateFailed)
	_ = stop()
	if got.ErrorCode != "input-changed" {
		t.Fatalf("%+v", got)
	}
}

func TestRunnerTimesOutLongJob(t *testing.T) {
	s, _ := newStore(t)
	job := submit(t, s, "k", []byte("x"))
	r := fastRunner(s, func(ctx context.Context, _ *Task) (Result, error) { <-ctx.Done(); return Result{}, ctx.Err() })
	r.Timeout = 50 * time.Millisecond
	stop := start(t, r)
	got := waitState(t, s, job.ID, StateFailed)
	_ = stop()
	if got.ErrorCode != "timeout" {
		t.Fatalf("%+v", got)
	}
}

func TestRunnerHonorsCancelOfRunningJob(t *testing.T) {
	s, _ := newStore(t)
	job := submit(t, s, "k", []byte("x"))
	var started atomic.Bool
	r := fastRunner(s, func(ctx context.Context, _ *Task) (Result, error) {
		started.Store(true)
		<-ctx.Done()
		return Result{}, ctx.Err()
	})
	stop := start(t, r)
	for !started.Load() {
		time.Sleep(time.Millisecond)
	}
	if _, err := s.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, job.ID, StateCanceled)
	_ = stop()
}

func TestRunnerRecoversStaleRunningJobOnStart(t *testing.T) {
	s, _ := newStore(t)
	job := submit(t, s, "k", []byte("x"))
	if _, err := s.Claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	stop := start(t, fastRunner(s, SHA256V1))
	waitState(t, s, job.ID, StateInterrupted)
	_ = stop()
}

func TestRunnerRunsJobsOneAfterAnother(t *testing.T) {
	s, _ := newStore(t)
	var running, peak atomic.Int32
	r := fastRunner(s, func(ctx context.Context, task *Task) (Result, error) {
		n := running.Add(1)
		defer running.Add(-1)
		if n > peak.Load() {
			peak.Store(n)
		}
		time.Sleep(10 * time.Millisecond)
		return SHA256V1(ctx, task)
	})
	var ids []string
	for _, k := range []string{"ka", "kb", "kc"} {
		job, _, err := s.Submit(context.Background(), SubmitRequest{Key: k, Name: "a.txt", Data: []byte(k), Submitter: Submitter{User: k, UID: "1025"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, job.ID)
	}
	stop := start(t, r)
	for _, id := range ids {
		waitState(t, s, id, StateSucceeded)
	}
	_ = stop()
	if peak.Load() != 1 {
		t.Fatalf("at most one job may run at a time, saw %d", peak.Load())
	}
}

func TestNewRunnerUsesProductLimitsAndTheFixedWorker(t *testing.T) {
	r := NewRunner(t.TempDir(), WorkerInfo{UID: 5})
	if r.Timeout != JobTimeout || r.Work == nil || r.Info.UID != 5 || r.Poll <= 0 || r.CancelPoll <= 0 || r.Beat <= 0 {
		t.Fatalf("%+v", r)
	}
	res, err := r.Work(context.Background(), &Task{Input: strings.NewReader("abc")})
	if err != nil || res.SHA256 != digest([]byte("abc")) {
		t.Fatalf("the production worker must be sha256-v1: %+v %v", res, err)
	}
}
