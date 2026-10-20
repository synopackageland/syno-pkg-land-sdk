package background

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var bob = Submitter{User: "bob", UID: "1026"}

func TestPauseQueuedJobHoldsItUntilResume(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	paused, err := s.PauseFor(ctx, job.ID, Access{Owner: admin})
	if err != nil || paused.State != StatePaused || paused.PauseReason != PauseUser || paused.PausedAt.IsZero() {
		t.Fatalf("%+v %v", paused, err)
	}
	if claimed, err := s.Claim(ctx); claimed != nil || err != nil {
		t.Fatalf("a paused job is never claimed: %+v %v", claimed, err)
	}
	if st, _ := s.Status(ctx); st.Paused != 1 || st.Queued != 0 {
		t.Fatalf("%+v", st)
	}
	again, err := s.PauseFor(ctx, job.ID, Access{Owner: admin})
	if err != nil || again.State != StatePaused {
		t.Fatalf("pausing twice is idempotent: %+v %v", again, err)
	}
	resumed, err := s.ResumeFor(ctx, job.ID, Access{Owner: admin})
	if err != nil || resumed.State != StateQueued || resumed.PauseReason != "" || !resumed.PausedAt.IsZero() {
		t.Fatalf("%+v %v", resumed, err)
	}
	if claimed, err := s.Claim(ctx); claimed == nil || claimed.ID != job.ID || err != nil {
		t.Fatalf("%+v %v", claimed, err)
	}
}

func TestPauseOfRunningJobIsARequestTheWorkerHonors(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	if _, err := s.Claim(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.PauseFor(ctx, job.ID, Access{Admin: true})
	if err != nil || got.State != StateRunning || !got.PauseRequested {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = s.ResumeFor(ctx, job.ID, Access{Admin: true})
	if err != nil || got.State != StateRunning || got.PauseRequested {
		t.Fatalf("resume withdraws the request: %+v %v", got, err)
	}
}

func TestPauseAndResumeRefuseFinishedDelegatedAndForeignJobs(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	done := submit(t, s, "done", []byte("x"))
	if _, err := s.Cancel(ctx, done.ID); err != nil {
		t.Fatal(err)
	}
	for _, f := range []func(context.Context, string, Access) (Job, error){s.PauseFor, s.ResumeFor} {
		if _, err := f(ctx, done.ID, Access{Admin: true}); !errors.Is(err, ErrNotPausable) {
			t.Fatalf("finished: %v", err)
		}
	}
	mine := submit(t, s, "mine", []byte("y"))
	if _, err := s.PauseFor(ctx, mine.ID, Access{Owner: bob}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another owner: %v", err)
	}
	delegated, err := s.SubmitDelegated(ctx, SubmitRequest{Key: "d", Name: "d", Data: []byte(`{}`), Submitter: bob},
		func(string) (DelegationBinding, error) { return DelegationBinding{DelegationID: "grant"}, nil }, func(DelegationBinding) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PauseFor(ctx, delegated.ID, Access{Admin: true}); !errors.Is(err, ErrNotPausable) {
		t.Fatalf("delegated jobs keep their own executor recovery: %v", err)
	}
}

func TestCancelEndsAPausedJob(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	if _, err := s.PauseFor(ctx, job.ID, Access{Admin: true}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Cancel(ctx, job.ID)
	if err != nil || got.State != StateCanceled {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestFinishPausedKeepsInputProgressAndCheckpoint(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	if _, err := s.Claim(ctx); err != nil {
		t.Fatal(err)
	}
	progress := &Progress{Done: 3, Total: 10, Bytes: 300, TotalBytes: 1000, Item: "/photo/c.jpg"}
	if err := s.Finish(ctx, job.ID, Outcome{State: StatePaused, PauseReason: PauseUser, Progress: progress, Checkpoint: "3"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, job.ID)
	if got.State != StatePaused || got.Checkpoint != "3" || got.Progress == nil || got.Progress.Done != 3 || got.Progress.Item != "/photo/c.jpg" || got.Progress.UpdatedAt.IsZero() {
		t.Fatalf("%+v %+v", got, got.Progress)
	}
	if _, err := s.ResumeFor(ctx, job.ID, Access{Admin: true}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(ctx)
	if err != nil || claimed == nil || claimed.Checkpoint != "3" {
		t.Fatalf("%+v %v", claimed, err)
	}
	f, err := s.OpenInput(job.ID)
	if err != nil {
		t.Fatalf("the input survives a pause: %v", err)
	}
	f.Close()
	if err := s.Finish(ctx, job.ID, Outcome{State: StateSucceeded, Result: &Result{SHA256: "h", Bytes: 1}, Progress: &Progress{Done: 10, Total: 10}}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, job.ID)
	if got.Checkpoint != "" || got.Progress.Done != 10 {
		t.Fatalf("a finished job keeps its last progress and drops its checkpoint: %+v", got)
	}
}

func TestPauseRequestedBeforeCancelLosesToCancel(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	s.Claim(ctx)
	s.PauseFor(ctx, job.ID, Access{Admin: true})
	s.Cancel(ctx, job.ID)
	if err := s.Finish(ctx, job.ID, Outcome{State: StatePaused, PauseReason: PauseUser}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, job.ID); got.State != StateCanceled {
		t.Fatalf("%+v", got)
	}
}

func TestReportPublishesProgressAndCheckpointOfTheRunningJob(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	if _, err := s.Report(ctx, job.ID, &Progress{Done: 1}, "1"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("%v", err)
	}
	s.Claim(ctx)
	s.PauseFor(ctx, job.ID, Access{Admin: true})
	got, err := s.Report(ctx, job.ID, &Progress{Done: 1, Total: 2, Item: strings.Repeat("é", MaxProgressItemBytes)}, "1")
	if err != nil || !got.PauseRequested || got.Progress.Done != 1 || got.Checkpoint != "1" {
		t.Fatalf("%+v %v", got, err)
	}
	if len(got.Progress.Item) > MaxProgressItemBytes || !strings.HasPrefix(strings.Repeat("é", MaxProgressItemBytes), got.Progress.Item) {
		t.Fatalf("the item is cut at a character boundary within %d bytes: %d", MaxProgressItemBytes, len(got.Progress.Item))
	}
	if _, err := s.Report(ctx, job.ID, nil, strings.Repeat("x", MaxCheckpointBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
}

func TestRecoverResumesJobsPausedByAStopButNotByAUser(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	stopped := submit(t, s, "stopped", []byte("a"))
	s.Claim(ctx)
	if err := s.Finish(ctx, stopped.ID, Outcome{State: StatePaused, PauseReason: PauseStopped, Checkpoint: "c"}); err != nil {
		t.Fatal(err)
	}
	user := submit(t, s, "user", []byte("b"))
	s.PauseFor(ctx, user.ID, Access{Admin: true})
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, stopped.ID); got.State != StateQueued || got.Checkpoint != "c" || got.PauseReason != "" {
		t.Fatalf("%+v", got)
	}
	if got, _ := s.Get(ctx, user.ID); got.State != StatePaused || got.PauseReason != PauseUser {
		t.Fatalf("%+v", got)
	}
	newer := NewStore(s.dir)
	newer.revision = WorkerRevision + 1
	if err := newer.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := newer.Get(ctx, user.ID); got.State != StateInterrupted || got.ErrorCode != "worker-changed" || got.Checkpoint != "" {
		t.Fatalf("a checkpoint from another worker revision is never resumed: %+v", got)
	}
}

func TestPausingAJobPausedByAStopKeepsItPaused(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("a"))
	s.Claim(ctx)
	s.Finish(ctx, job.ID, Outcome{State: StatePaused, PauseReason: PauseStopped})
	got, err := s.PauseFor(ctx, job.ID, Access{Admin: true})
	if err != nil || got.PauseReason != PauseUser {
		t.Fatalf("%+v %v", got, err)
	}
	s.Recover(ctx)
	if got, _ := s.Get(ctx, job.ID); got.State != StatePaused {
		t.Fatalf("%+v", got)
	}
}

// countTo is a resumable Work: it counts to n, one step per tick, checkpointing
// each step, and stops when its context ends.
func countTo(n int, seen *atomic.Value, release <-chan struct{}) Work {
	return func(ctx context.Context, task *Task) (Result, error) {
		seen.Store(task.Job.Checkpoint)
		i := 0
		if task.Job.Checkpoint != "" {
			i = int(task.Job.Checkpoint[0] - '0')
		}
		for ; i < n; i++ {
			task.Report(Progress{Done: int64(i), Total: int64(n), Item: "step"})
			if err := task.Checkpoint(string(rune('0' + i))); err != nil {
				return Result{}, err
			}
			if i == 2 {
				select {
				case <-ctx.Done():
					return Result{}, ctx.Err()
				case <-release:
				}
			}
		}
		task.Report(Progress{Done: int64(n), Total: int64(n)})
		return HashInput(ctx, task.Input)
	}
}

func TestRunnerPausesAtACheckpointAndResumesFromIt(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	var seen atomic.Value
	release := make(chan struct{})
	stop := start(t, fastRunner(s, countTo(5, &seen, release)))
	defer stop()
	waitFor(t, func() bool { got, _ := s.Get(ctx, job.ID); return got.Progress != nil && got.Progress.Done == 2 })
	if _, err := s.PauseFor(ctx, job.ID, Access{Admin: true}); err != nil {
		t.Fatal(err)
	}
	paused := waitState(t, s, job.ID, StatePaused)
	if paused.Checkpoint != "2" || paused.PauseReason != PauseUser || paused.Progress.Done != 2 || paused.Progress.Total != 5 {
		t.Fatalf("%+v %+v", paused, paused.Progress)
	}
	close(release)
	if _, err := s.ResumeFor(ctx, job.ID, Access{Admin: true}); err != nil {
		t.Fatal(err)
	}
	done := waitState(t, s, job.ID, StateSucceeded)
	if seen.Load() != "2" || done.Progress.Done != 5 || done.Result == nil {
		t.Fatalf("resumed from %v: %+v", seen.Load(), done)
	}
}

func TestPackageStopPausesTheRunningJobAndStartResumesIt(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	var seen atomic.Value
	release := make(chan struct{})
	stop := start(t, fastRunner(s, countTo(5, &seen, release)))
	waitFor(t, func() bool { got, _ := s.Get(ctx, job.ID); return got.Progress != nil && got.Progress.Done == 2 })
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, job.ID); got.State != StatePaused || got.PauseReason != PauseStopped || got.Checkpoint != "2" {
		t.Fatalf("%+v", got)
	}
	close(release)
	stop = start(t, fastRunner(s, countTo(5, &seen, release)))
	defer stop()
	waitState(t, s, job.ID, StateSucceeded)
	if seen.Load() != "2" {
		t.Fatalf("resumed from %v", seen.Load())
	}
}

func TestTaskRefusesOversizedCheckpoint(t *testing.T) {
	task := &Task{}
	if err := task.Checkpoint(strings.Repeat("x", MaxCheckpointBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
	if err := task.Checkpoint("\xff"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid UTF-8: %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never held")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSchemaOneJournalReadsAndCommitsAsSchemaThree(t *testing.T) {
	s, dir := newStore(t)
	submit(t, s, "k", []byte("x"))
	journalPath := dir + "/journal.json"
	b, _ := os.ReadFile(journalPath)
	if !bytes.Contains(b, []byte(`"schema":3`)) {
		t.Fatalf("%s", b)
	}
	if err := os.WriteFile(journalPath, bytes.Replace(b, []byte(`"schema":3`), []byte(`"schema":1`), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if jobs, err := s.List(context.Background()); err != nil || len(jobs) != 1 {
		t.Fatalf("%v %v", jobs, err)
	}
	submit(t, s, "k2", []byte("y"))
	if b, _ := os.ReadFile(journalPath); !bytes.Contains(b, []byte(`"schema":3`)) {
		t.Fatalf("%s", b)
	}
	if err := os.WriteFile(journalPath, []byte(`{"schema":4,"workerRevision":1,"jobs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background()); !errors.Is(err, ErrJournalSchema) {
		t.Fatalf("%v", err)
	}
}

func TestDisablingAScheduleCancelsItsPausedJobs(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	job := submit(t, s, "k", []byte("x"))
	s.PauseFor(ctx, job.ID, Access{Admin: true})
	s.withJournal(ctx, func(root *os.Root, j *journal) (bool, error) {
		find(j, job.ID).ScheduleID = "s"
		s.cancelScheduleJobs(root, j, "s")
		return true, nil
	})
	if got, _ := s.Get(ctx, job.ID); got.State != StateCanceled || got.ErrorCode != "schedule-disabled" {
		t.Fatalf("%+v", got)
	}
}
