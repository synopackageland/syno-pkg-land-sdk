package delegate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func wantCanceled(t *testing.T, out OperationResult, err error) {
	t.Helper()
	if err != nil || out.State != "canceled" || out.Code != "OPERATION_CANCELED" || out.Reason != "operation-canceled-by-user" {
		t.Fatal("want canceled", out, err)
	}
}

// A cancel of a reference that was reserved but never admitted must stop the
// SDK's own admission recovery from sending it later.
func TestCancelOfUnadmittedReferenceIsNeverSentByRecovery(t *testing.T) {
	s, in, e, clock := recoveringService(t)
	reserveWithoutSending(t, s, in)
	ref := OperationRef{in.DelegationID, in.JobID}
	out, err := s.CancelOperation(context.Background(), nil, ref)
	wantCanceled(t, out, err)
	clock.at = clock.at.Add(time.Hour)
	for i := 0; i < 2; i++ {
		out, err := s.MoveFromStaging(context.Background(), in)
		if err != nil || out.State != "canceled" {
			t.Fatal("same-ref call after cancel", out, err)
		}
		if err = restarted(s, e, clock).SweepMoves(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if moves, created := e.counts(); moves != 0 || created != 0 {
		t.Fatal("canceled reference was admitted", moves, created)
	}
	if _, err = os.Stat(filepath.Join(s.StagingDir, in.Staging)); err != nil {
		t.Fatal("canceled source removed", err)
	}
}

// The intent survives an executor that cannot be reached at cancel time: the
// package's next same-ref call or sweep forwards the cancel, never admission.
func TestCancelIntentSurvivesUnavailableExecutorAndRestart(t *testing.T) {
	s, in, e, clock := recoveringService(t)
	reserveWithoutSending(t, s, in)
	dir, err := os.MkdirTemp("/tmp", "t0205-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s.Client = &Client{Socket: filepath.Join(dir, "absent.sock"), Timeout: time.Second}
	_, err = s.CancelOperation(context.Background(), nil, OperationRef{in.DelegationID, in.JobID})
	code(t, err, "EXECUTOR_UNAVAILABLE")
	next := restarted(s, e, clock)
	if err = next.SweepMoves(context.Background()); err != nil {
		t.Fatal(err)
	}
	if moves, created := e.counts(); moves != 0 || created != 0 || e.cancels != 1 {
		t.Fatal("sweep admitted a canceled reference", moves, created, e.cancels)
	}
	if out, err := next.MoveFromStaging(context.Background(), in); err != nil || out.State != "canceled" {
		t.Fatal(out, err)
	}
	if pending, err := next.Store.PendingMoves(context.Background()); err != nil || len(pending) != 0 {
		t.Fatal("canceled mirror still swept", pending, err)
	}
}

// A cancel whose reply was lost is repeated with the same reference.
func TestRepeatedCancelAfterLostReplyIsIdempotent(t *testing.T) {
	s, in, e, _ := recoveringService(t)
	if _, err := s.MoveFromStaging(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	ref := OperationRef{in.DelegationID, in.JobID}
	s.Client = fakeExecutor(func(ctx context.Context, op string, auth map[string]string, p, out any) error {
		if err := e.Call(ctx, op, auth, p, out); err != nil || op != "files.cancelOperation" {
			return err
		}
		return fail("EXECUTOR_TIMEOUT")
	})
	_, err := s.CancelOperation(context.Background(), nil, ref)
	code(t, err, "EXECUTOR_TIMEOUT")
	s.Client = e
	for i := 0; i < 2; i++ {
		out, err := s.CancelOperation(context.Background(), nil, ref)
		wantCanceled(t, out, err)
	}
	if moves, created := e.counts(); moves != 1 || created != 1 {
		t.Fatal(moves, created)
	}
}
