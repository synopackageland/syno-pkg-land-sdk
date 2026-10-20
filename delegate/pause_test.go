package delegate

import (
	"context"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/background"
)

// stepWork reports and checkpoints one step, then waits for release or its
// context; it resumes after its checkpoint.
func stepWork(started chan<- string, release <-chan struct{}) background.Work {
	return func(ctx context.Context, task *background.Task) (background.Result, error) {
		started <- task.Job.Checkpoint
		task.Report(background.Progress{Done: 1, Total: 2, Item: "first"})
		if err := task.Checkpoint("1"); err != nil {
			return background.Result{}, err
		}
		select {
		case <-ctx.Done():
			return background.Result{}, ctx.Err()
		case <-release:
		}
		task.Report(background.Progress{Done: 2, Total: 2})
		return background.HashInput(ctx, task.Input)
	}
}

var alice = background.Submitter{User: "alice", UID: "1205"}

func runOnceAsync(s *Service, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- s.RunOnce(ctx) }()
	return done
}

func waitDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunOnce did not return")
	}
}

// A job of the App's own worker pauses at the user's request and on a
// package stop, as under background.Runner, and resumes from its checkpoint.
func TestServiceRunsTheAppsWorkWithPauseAndCheckpoint(t *testing.T) {
	for _, how := range []string{"user", "stopped"} {
		t.Run(how, func(t *testing.T) {
			s := service(t, nil)
			started, release := make(chan string, 2), make(chan struct{})
			s.Work = stepWork(started, release)
			ctx := context.Background()
			job, _, err := s.Store.Submit(ctx, background.SubmitRequest{Key: "k", Name: "a.txt", Data: []byte("x"), Submitter: alice})
			if err != nil {
				t.Fatal(err)
			}
			parent, stop := context.WithCancel(ctx)
			defer stop()
			done := runOnceAsync(s, parent)
			if from := <-started; from != "" {
				t.Fatal("first run resumed from", from)
			}
			if how == "user" {
				if _, err := s.Store.PauseFor(ctx, job.ID, background.Access{Admin: true}); err != nil {
					t.Fatal(err)
				}
			} else {
				time.Sleep(150 * time.Millisecond)
				stop()
			}
			waitDone(t, done)
			got, _ := s.Store.Get(ctx, job.ID)
			if got.State != background.StatePaused || got.PauseReason != how || got.Checkpoint != "1" || got.Progress == nil || got.Progress.Done != 1 {
				t.Fatalf("%+v", got)
			}
			if how == "user" {
				if _, err := s.Store.ResumeFor(ctx, job.ID, background.Access{Admin: true}); err != nil {
					t.Fatal(err)
				}
			} else if err := s.Store.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			close(release)
			done = runOnceAsync(s, ctx)
			if from := <-started; from != "1" {
				t.Fatal("resumed from", from)
			}
			waitDone(t, done)
			if got, _ := s.Store.Get(ctx, job.ID); got.State != background.StateSucceeded || got.Result == nil {
				t.Fatalf("%+v", got)
			}
		})
	}
}

// Without its own Work the Service hashes the input, as before.
func TestServiceDefaultWorkIsSHA256(t *testing.T) {
	s := service(t, nil)
	ctx := context.Background()
	job, _, err := s.Store.Submit(ctx, background.SubmitRequest{Key: "k", Name: "a.txt", Data: []byte("x"), Submitter: alice})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Store.Get(ctx, job.ID); got.State != background.StateSucceeded || got.Result == nil || got.Result.SHA256 != job.InputDigest {
		t.Fatalf("%+v", got)
	}
}
