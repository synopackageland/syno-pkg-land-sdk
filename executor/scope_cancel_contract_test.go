package main

import (
	"context"
	"errors"
	"testing"
)

// killedChildWriter models the subprocess writer: its child dies with the
// context and the copy ends with a transport error, before any publish guard.
type killedChildWriter struct{ copying chan struct{} }

func (w killedChildWriter) Write(context.Context, Subject, string, string, string, string) (MoveReceipt, error) {
	return MoveReceipt{}, nil
}
func (w killedChildWriter) Move(ctx context.Context, _ Subject, _ Operation, _ Delegation, _ func(func() (MoveReceipt, error)) (MoveReceipt, error)) (MoveReceipt, error) {
	close(w.copying)
	<-ctx.Done()
	return MoveReceipt{}, errors.New("child copy")
}

func TestStopDuringCopySettlesExecutorPausedNotWriteFailed(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	w := killedChildWriter{copying: make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := offline(e, "files.moveFromStaging", moveParams(id, "job")); !r.OK {
		t.Fatal(r)
	}
	<-w.copying
	e.stopOperations()
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	// An executor stop is not an I/O failure of the destination.
	wantOperation(t, offline(e, "files.operationStatus", map[string]any{"delegation_id": id, "job_id": "job"}), "interrupted", "EXECUTOR_PAUSED", "")
}

func TestScopeChangeCancelsCopyDurablyAcrossStop(t *testing.T) {
	e, _ := selectedExecutor(t)
	if r := selectScope(e, "home", 0); !r.OK {
		t.Fatal(r)
	}
	d := selectedGrant(t, e, "/home")
	w := newStubbornWriter()
	e.Writer = w
	startedCopy(t, e, w, d.ID, "job")
	if r := selectScope(e, "shared:reports", 0); !r.OK {
		t.Fatal(r)
	}
	ref := map[string]any{"delegation_id": d.ID, "job_id": "job"}
	wantOperation(t, offline(e, "files.operationStatus", ref), "copying", "CANCEL_REQUESTED", "scope-changed")
	stopped := make(chan struct{})
	go func() { e.stopOperations(); close(stopped) }()
	w.let()
	<-stopped
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	wantOperation(t, offline(e, "files.operationStatus", ref), "canceled", "OPERATION_CANCELED", "scope-changed")
	select {
	case <-w.published:
		t.Fatal("published to a revoked destination")
	default:
	}
}

func TestScopeChangeCancelRecordedBeforeCrashSettlesCanceled(t *testing.T) {
	e, _ := selectedExecutor(t)
	if r := selectScope(e, "home", 0); !r.OK {
		t.Fatal(r)
	}
	d := selectedGrant(t, e, "/home")
	w := newStubbornWriter()
	e.Writer = w
	startedCopy(t, e, w, d.ID, "job")
	if r := selectScope(e, "shared:reports", 0); !r.OK {
		t.Fatal(r)
	}
	e.commitMu.Lock()
	op, err := e.Store.GetOperation("job")
	e.commitMu.Unlock()
	if err != nil || op.CancelRequested != "scope-changed" {
		t.Fatal("scope cancellation not durable before the writer stops", op, err)
	}
}
