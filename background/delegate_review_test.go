package background

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReviewGrantCreationDoesNotBlockOtherOwnersCancel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	worker := NewStore(dir)
	cgi := NewRequestStore(dir)
	cgi.lockTimeout = 20 * time.Millisecond
	ctx := context.Background()
	alice := Submitter{User: "alice", UID: "1205"}
	bob := Submitter{User: "bob", UID: "1206"}
	j, _, err := worker.Submit(ctx, SubmitRequest{Key: "existing", Name: "hash", Data: []byte("x"), Submitter: alice})
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := cgi.SubmitDelegated(ctx, SubmitRequest{Key: "download", Name: "file", Data: []byte("x"), Submitter: bob}, func(string) (DelegationBinding, error) {
			close(started)
			<-release
			return DelegationBinding{DelegationID: "grant"}, nil
		}, func(DelegationBinding) {})
		done <- err
	}()
	<-started
	_, err = cgi.CancelFor(ctx, j.ID, Access{Owner: alice})
	close(release)
	if err2 := <-done; err2 != nil {
		t.Fatal(err2)
	}
	if errors.Is(err, ErrBusy) {
		t.Fatal("another owner's DSM grant request blocks cancellation under global jobs lock")
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestDelegatedReservationCannotRunAndCanceledGrantIsRevoked(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "spool"))
	ctx := context.Background()
	owner := Submitter{User: "alice", UID: "1205"}
	req := SubmitRequest{Key: "pending", Name: "file", Data: []byte("input"), Submitter: owner}
	rollback := 0
	_, err := s.SubmitDelegated(ctx, req, func(id string) (DelegationBinding, error) {
		if claimed, e := s.Claim(ctx); e != nil || claimed != nil {
			t.Fatalf("claimed unfinished reservation: %+v %v", claimed, e)
		}
		if _, e := s.SubmitDelegated(ctx, req, func(string) (DelegationBinding, error) {
			t.Fatal("duplicate grant created")
			return DelegationBinding{DelegationID: ""}, nil
		}, func(DelegationBinding) {}); !errors.Is(e, ErrBusy) {
			t.Fatal("inflight duplicate must be busy", e)
		}
		if _, e := s.CancelFor(ctx, id, Access{Owner: owner}); e != nil {
			t.Fatal(e)
		}
		return DelegationBinding{DelegationID: "grant"}, nil
	}, func(b DelegationBinding) {
		rollback++
		if b.DelegationID != "grant" {
			t.Fatal(b)
		}
		if _, e := s.Delegation(ctx, b.JobID); e != nil {
			t.Fatal("revocation not durable", e)
		}
	})
	if !errors.Is(err, ErrNotCancelable) || rollback != 1 {
		t.Fatal("canceled grant committed", err, rollback)
	}
	pending, e := s.DelegationCleanup(ctx)
	if e != nil || len(pending) != 1 {
		t.Fatal(pending, e)
	}
}

func TestDelegatedCommitFailureRevokesOutsideLock(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "spool"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rollback := 0
	_, err := s.SubmitDelegated(ctx, SubmitRequest{Key: "cancel-context", Name: "file", Data: []byte("input"), Submitter: Submitter{User: "alice", UID: "1205"}}, func(string) (DelegationBinding, error) {
		cancel()
		return DelegationBinding{DelegationID: "grant"}, nil
	}, func(b DelegationBinding) {
		rollback++
		if _, e := s.Get(context.Background(), b.JobID); e != nil {
			t.Fatal("rollback ran under journal lock", e)
		}
	})
	if !errors.Is(err, context.Canceled) || rollback != 1 {
		t.Fatal(err, rollback)
	}
	pending, e := s.DelegationCleanup(context.Background())
	if e != nil || len(pending) != 1 {
		t.Fatal("grant cleanup binding lost", pending, e)
	}
}

func TestFailedGrantCreationReleasesReservation(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "spool"))
	ctx := context.Background()
	failure := errors.New("DSM unavailable")
	var id string
	_, err := s.SubmitDelegated(ctx, SubmitRequest{Key: "failed", Name: "file", Data: []byte("input"), Submitter: Submitter{User: "alice", UID: "1205"}}, func(job string) (DelegationBinding, error) {
		id = job
		return DelegationBinding{DelegationID: ""}, failure
	}, func(DelegationBinding) { t.Error("revoked nonexistent grant") })
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	job, e := s.Get(ctx, id)
	if e != nil || job.AdmissionPending || job.State != StateFailed {
		t.Fatal(job, e)
	}
}

func TestDelegatedReservationRecoveryFailsClosed(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "spool"))
	ctx := context.Background()
	revoked := false
	_, err := s.SubmitDelegated(ctx, SubmitRequest{Key: "recover", Name: "file", Data: []byte("input"), Submitter: Submitter{User: "alice", UID: "1205"}}, func(id string) (DelegationBinding, error) {
		if e := s.Recover(ctx); e != nil {
			t.Fatal(e)
		}
		job, e := s.Get(ctx, id)
		if e != nil || job.State != StateInterrupted || job.AdmissionPending {
			t.Fatal(job, e)
		}
		return DelegationBinding{DelegationID: "grant"}, nil
	}, func(DelegationBinding) { revoked = true })
	if !errors.Is(err, ErrNotCancelable) || !revoked {
		t.Fatal("recovered reservation committed", err, revoked)
	}
}

func TestDelegatedReservationsCountAgainstRevocationBacklog(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "spool"))
	ctx := context.Background()
	if err := s.withJournal(ctx, func(root *os.Root, _ *journal) (bool, error) {
		for i := 0; i < MaxHistory+MaxQueued-1; i++ {
			id := fmt.Sprintf("%032x", i)
			if err := writeAtomic(root, bindingName(id), DelegationBinding{JobID: id, DelegationID: "old-grant"}); err != nil {
				return false, err
			}
		}
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.SubmitDelegated(ctx, SubmitRequest{Key: "first", Name: "file", Data: []byte("input"), Submitter: Submitter{User: "alice", UID: "1205"}}, func(string) (DelegationBinding, error) {
		_, err := s.SubmitDelegated(ctx, SubmitRequest{Key: "second", Name: "file", Data: []byte("input"), Submitter: Submitter{User: "bob", UID: "1206"}}, func(string) (DelegationBinding, error) {
			t.Error("inflight reservation exceeded cleanup backlog bound")
			return DelegationBinding{DelegationID: "extra-grant"}, nil
		}, func(DelegationBinding) {})
		if !errors.Is(err, ErrQuota) {
			t.Errorf("pending reservation did not reserve cleanup capacity: %v", err)
		}
		return DelegationBinding{DelegationID: "grant"}, nil
	}, func(DelegationBinding) {})
	if err != nil {
		t.Fatal(err)
	}
}
