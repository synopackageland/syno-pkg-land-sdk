package delegate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func sourceExists(t *testing.T, s *Service, in MoveParams) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(s.StagingDir, in.Staging))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// An App deleting a job discards the staging of a move that will never run.
func TestDiscardStagingRemovesSourceOfCanceledMove(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	ref := OperationRef{in.DelegationID, in.JobID}
	if out, err := s.CancelOperation(context.Background(), nil, ref); err != nil || out.State != "canceled" {
		t.Fatal(out, err)
	}
	for i := 0; i < 2; i++ {
		if err := s.DiscardStaging(context.Background(), ref); err != nil {
			t.Fatal(err)
		}
	}
	if sourceExists(t, s, in) {
		t.Fatal("source kept")
	}
	if moves, _ := e.counts(); moves != 1 {
		t.Fatal("discard admitted again", moves)
	}
}

// A refused move whose grant was purged is discarded too.
func TestDiscardStagingAfterPurgedGrant(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	e.admit = func(context.Context, MoveParams) (bool, error) { return false, fail("SCOPE_DENIED") }
	_, err := s.MoveFromStaging(context.Background(), in)
	code(t, err, "SCOPE_DENIED")
	s.Client = purgedExecutor()
	ref := OperationRef{in.DelegationID, in.JobID}
	if _, err = s.CancelOperation(context.Background(), nil, ref); err != nil {
		t.Fatal(err)
	}
	if err = s.DiscardStaging(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if sourceExists(t, s, in) {
		t.Fatal("source kept")
	}
}

// A move that may still copy keeps its source.
func TestDiscardStagingRefusesActiveMove(t *testing.T) {
	s, in, _, _ := recoveringService(t)
	if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	err := s.DiscardStaging(context.Background(), OperationRef{in.DelegationID, in.JobID})
	code(t, err, "operation-active")
	if !sourceExists(t, s, in) {
		t.Fatal("active source removed")
	}
}

// A retry with a new job reuses the staging file: the old job cannot remove it.
func TestDiscardStagingKeepsSourceOfAnotherPendingJob(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	e.admit = func(_ context.Context, p MoveParams) (bool, error) {
		if p.JobID == in.JobID {
			return false, fail("PERMISSION_DENIED")
		}
		return true, nil
	}
	_, err := s.MoveFromStaging(context.Background(), in)
	code(t, err, "PERMISSION_DENIED")
	retry := in
	retry.JobID = in.JobID + ".a1"
	if _, err = s.MoveFromStaging(context.Background(), retry); err != nil {
		t.Fatal(err)
	}
	err = s.DiscardStaging(context.Background(), OperationRef{in.DelegationID, in.JobID})
	code(t, err, "STAGING_IN_USE")
	if !sourceExists(t, s, in) {
		t.Fatal("retry source removed")
	}
}

// A discarded source is finished history: its mirror says so, so retention
// can drop it like a committed one.
func TestDiscardStagingFinishesTheMirror(t *testing.T) {
	s, in, _, _ := recoveringService(t)
	if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	ref := OperationRef{in.DelegationID, in.JobID}
	if _, err := s.CancelOperation(context.Background(), nil, ref); err != nil {
		t.Fatal(err)
	}
	if err := s.DiscardStaging(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if binding, err := s.Store.MoveBinding(context.Background(), in.JobID); err != nil || !binding.CleanupDone {
		t.Fatal(binding, err)
	}
}
