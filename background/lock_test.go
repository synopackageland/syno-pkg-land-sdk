package background

import (
	"context"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func holdJournal(t *testing.T, s *Store) func() {
	t.Helper()
	root, err := s.openRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := root.OpenFile("lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	release, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		defer f.Close()
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		ready <- err
		if err != nil {
			return
		}
		defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
		<-release
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	return func() { close(release); <-done }
}
func TestRequestSubmitLockBudgetAndRetry(t *testing.T) {
	s := NewRequestStore(filepath.Join(t.TempDir(), "background"))
	// Exercise an explicit short contention limit without waiting for the default.
	const lockBudget = 50 * time.Millisecond
	s.lockTimeout = lockBudget
	release := holdJournal(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), lockBudget+time.Second)
	defer cancel()
	start := time.Now()
	req := SubmitRequest{Key: "retry", Name: "copy", Data: []byte("copy"), Submitter: admin}
	_, _, err := s.Submit(ctx, req)
	elapsed := time.Since(start)
	release()
	if !errors.Is(err, ErrBusy) || elapsed < lockBudget || elapsed > lockBudget+500*time.Millisecond {
		t.Fatalf("elapsed=%v err=%v", elapsed, err)
	}
	jobs, err := s.List(context.Background())
	if err != nil || len(jobs) != 0 {
		t.Fatalf("timed-out submit wrote: %+v %v", jobs, err)
	}
	first, dup, err := s.Submit(context.Background(), req)
	if err != nil || dup {
		t.Fatalf("retry: %v %v", dup, err)
	}
	again, dup, err := s.Submit(context.Background(), req)
	if err != nil || !dup || again.ID != first.ID {
		t.Fatalf("duplicate retry: %v %v", dup, err)
	}
}
func TestJournalCancellationDoesNotWaitOrMutate(t *testing.T) {
	for _, locked := range []bool{false, true} {
		t.Run(map[bool]string{false: "free", true: "held"}[locked], func(t *testing.T) {
			s := NewRequestStore(filepath.Join(t.TempDir(), "background"))
			if locked {
				release := holdJournal(t, s)
				defer release()
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			start := time.Now()
			_, _, err := s.Submit(ctx, SubmitRequest{Key: "canceled", Name: "copy", Data: []byte("copy"), Submitter: admin})
			if !errors.Is(err, context.Canceled) || time.Since(start) > 250*time.Millisecond {
				t.Fatalf("cancellation: %v", err)
			}
		})
	}
}
func TestWorkerCanWaitBeyondRequestLockBudget(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "background"))
	release := holdJournal(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := s.List(ctx)
	release()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("worker wait changed: %v", err)
	}
}

func TestRequestJournalWaitsBeyondFormerFiveSecondLimit(t *testing.T) {
	if RequestLockTimeout != runtimebudget.Operation {
		t.Fatal("journal admission must share the operation default")
	}
	s := NewRequestStore(filepath.Join(t.TempDir(), "background"))
	release := holdJournal(t, s)
	released := make(chan struct{})
	go func() {
		time.Sleep(6 * time.Second)
		release()
		close(released)
	}()
	defer func() { <-released }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	job, duplicate, err := s.Submit(ctx, SubmitRequest{Key: "slow-lock", Name: "copy", Data: []byte("copy"), Submitter: admin})
	if err != nil || duplicate || job.ID == "" {
		t.Fatalf("slow journal admission: job=%+v duplicate=%v err=%v", job, duplicate, err)
	}
}

func TestRequestLockWaitHonorsEarlierDeadlineAndCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			s := NewRequestStore(filepath.Join(t.TempDir(), "background"))
			release := holdJournal(t, s)
			defer release()
			var ctx context.Context
			var cancel context.CancelFunc
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := s.List(ctx); done <- err }()
			if !deadline {
				time.AfterFunc(50*time.Millisecond, cancel)
			}
			want := context.Canceled
			if deadline {
				want = context.DeadlineExceeded
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("got %v want %v", err, want)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("context did not end lock wait")
			}
		})
	}
}
