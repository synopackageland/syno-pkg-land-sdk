package delegate

import (
	"context"
	"testing"
)

// purgedExecutor answers like an executor whose store lost the grant, for
// example after an uninstall purge: it knows neither the grant nor the job.
func purgedExecutor() ExecutorClient {
	return fakeExecutor(func(context.Context, string, map[string]string, any, any) error {
		return fail(CodeOperationNotFound)
	})
}

// A move the executor refused at admission never ran; once its grant is gone
// the executor cannot record the cancel, so the SDK settles it.
func TestCancelOfRefusedMoveWhoseGrantWasPurgedSettles(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	e.admit = func(context.Context, MoveParams) (bool, error) { return false, fail("DELEGATION_DENIED") }
	_, err := s.MoveFromStaging(context.Background(), in)
	code(t, err, "DELEGATION_DENIED")
	s.Client = purgedExecutor()
	for i := 0; i < 2; i++ {
		out, err := s.CancelOperation(context.Background(), nil, OperationRef{in.DelegationID, in.JobID})
		wantCanceled(t, out, err)
	}
}

// A cancel recorded before the purge is settled by the next sweep instead of
// being forwarded forever.
func TestSweepSettlesCancelWhoseGrantWasPurged(t *testing.T) {
	s, in, _, clock := recoveringService(t)
	reserveWithoutSending(t, s, in)
	s.Client = purgedExecutor()
	_, _ = s.CancelOperation(context.Background(), nil, OperationRef{in.DelegationID, in.JobID})
	next := restarted(s, purgedExecutor(), clock)
	if err := next.SweepMoves(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending, err := next.Store.PendingMoves(context.Background()); err != nil || len(pending) != 0 {
		t.Fatal("purged cancel still swept", pending, err)
	}
}

// A session caller is answered by the executor alone: operation-not-found
// there may mean another user's operation.
func TestSessionCancelOfUnknownOperationIsNotSettled(t *testing.T) {
	s, in, _, _ := recoveringService(t)
	reserveWithoutSending(t, s, in)
	_ = s.Store.RequestMoveCancel(context.Background(), in.JobID, in.DelegationID)
	s.Client = purgedExecutor()
	_, err := s.CancelOperation(context.Background(), map[string]string{"dsm_cookie": "other"}, OperationRef{in.DelegationID, in.JobID})
	code(t, err, CodeOperationNotFound)
}
