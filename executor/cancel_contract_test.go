package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubbornMoveWriter keeps copying after its context ends until released, like
// a slow write syscall, so the canceling window can be observed.
type stubbornMoveWriter struct {
	copying, release, published chan struct{}
	publishing, finishPublish   chan struct{}
	once                        sync.Once
}

func (w *stubbornMoveWriter) let() { w.once.Do(func() { close(w.release) }) }

func newStubbornWriter() *stubbornMoveWriter {
	return &stubbornMoveWriter{copying: make(chan struct{}), release: make(chan struct{}), published: make(chan struct{})}
}
func (w *stubbornMoveWriter) Write(context.Context, Subject, string, string, string, string) (MoveReceipt, error) {
	return MoveReceipt{}, nil
}
func (w *stubbornMoveWriter) Move(_ context.Context, _ Subject, op Operation, d Delegation, guard func(func() (MoveReceipt, error)) (MoveReceipt, error)) (MoveReceipt, error) {
	close(w.copying)
	<-w.release
	return guard(func() (MoveReceipt, error) {
		if w.publishing != nil {
			close(w.publishing)
			<-w.finishPublish
		}
		close(w.published)
		return MoveReceipt{Path: d.Path + "/" + op.Name}, nil
	})
}

func operationOf(t *testing.T, r Response) map[string]any {
	t.Helper()
	if !r.OK {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r.Result)
	out := map[string]any{}
	json.Unmarshal(b, &out)
	return out
}
func wantOperation(t *testing.T, r Response, state, code, reason string) {
	t.Helper()
	out := operationOf(t, r)
	got, _ := out["code"].(string)
	why, _ := out["reason"].(string)
	if out["state"] != state || got != code || why != reason {
		t.Fatalf("want %s/%s/%s, got %v", state, code, reason, out)
	}
}
func startedCopy(t *testing.T, e *Executor, w *stubbornMoveWriter, id, job string) {
	t.Helper()
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Cleanups run in reverse: release the writer, then stop.
	t.Cleanup(e.stopOperations)
	t.Cleanup(w.let)
	if r := offline(e, "files.moveFromStaging", moveParams(id, job)); !r.OK {
		t.Fatal(r)
	}
	select {
	case <-w.copying:
	case <-time.After(2 * time.Second):
		t.Fatal("copy did not start")
	}
}

func TestCancelCopyingAcknowledgesDurablyUntilCanceled(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	w := newStubbornWriter()
	e.Writer = w
	startedCopy(t, e, w, id, "job")
	ref := map[string]any{"delegation_id": id, "job_id": "job"}
	for i := 0; i < 2; i++ {
		// A repeated cancel after a lost reply returns the same acknowledgement.
		wantOperation(t, offline(e, "files.cancelOperation", ref), "copying", "CANCEL_REQUESTED", "operation-canceled-by-user")
	}
	wantOperation(t, offline(e, "files.operationStatus", ref), "copying", "CANCEL_REQUESTED", "operation-canceled-by-user")
	e.commitMu.Lock()
	op, err := e.Store.GetOperation("job")
	e.commitMu.Unlock()
	if err != nil || op.CancelRequested != "operation-canceled-by-user" {
		t.Fatal("cancel intent not durable", op, err)
	}
	w.let()
	waitState(t, e, "job", "canceled")
	select {
	case <-w.published:
		t.Fatal("published after cancel acknowledgement")
	default:
	}
	wantOperation(t, offline(e, "files.cancelOperation", ref), "canceled", "OPERATION_CANCELED", "operation-canceled-by-user")
	wantOperation(t, offline(e, "files.moveFromStaging", moveParams(id, "job")), "canceled", "OPERATION_CANCELED", "operation-canceled-by-user")
}

func TestCancelRequestedCopySettlesCanceledAcrossStopAndCrash(t *testing.T) {
	t.Run("stop-while-canceling", func(t *testing.T) {
		e, _, _ := setup(t)
		id := longGrant(t, e)
		w := newStubbornWriter()
		e.Writer = w
		startedCopy(t, e, w, id, "job")
		ref := map[string]any{"delegation_id": id, "job_id": "job"}
		wantOperation(t, offline(e, "files.cancelOperation", ref), "copying", "CANCEL_REQUESTED", "operation-canceled-by-user")
		stopped := make(chan struct{})
		go func() { e.stopOperations(); close(stopped) }()
		w.let()
		<-stopped
		if err := e.startOperations(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer e.stopOperations()
		wantOperation(t, offline(e, "files.operationStatus", ref), "canceled", "OPERATION_CANCELED", "operation-canceled-by-user")
	})
	for _, intent := range []bool{false, true} {
		name := map[bool]string{false: "crash-before-publish", true: "crash-after-publish-intent"}[intent]
		t.Run(name, func(t *testing.T) {
			e, _, _ := setup(t)
			id := longGrant(t, e)
			op := Operation{Version: 1, Package: e.Policy.Package, ID: id, Job: "job", State: "copying", CancelRequested: "operation-canceled-by-user", PublishIntent: intent, Staging: "result", Name: "out.csv", Size: 123}
			if e.Store.PutOperation(op) != nil {
				t.Fatal("put")
			}
			w := newStubbornWriter()
			e.Writer = w
			if err := e.startOperations(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer e.stopOperations()
			ref := map[string]any{"delegation_id": id, "job_id": "job"}
			if intent {
				// A publish that may have happened is never reported as canceled.
				wantOperation(t, offline(e, "files.operationStatus", ref), "uncertain", "uncertain", "")
				return
			}
			wantOperation(t, offline(e, "files.operationStatus", ref), "canceled", "OPERATION_CANCELED", "operation-canceled-by-user")
		})
	}
}

func TestCancelBeforeAdmissionLeavesTombstoneThatIsNeverAdmitted(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	w := newStubbornWriter()
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	ref := map[string]any{"delegation_id": id, "job_id": "job"}
	if r := offline(e, "files.operationStatus", ref); r.Code != "operation-not-found" {
		t.Fatal(r)
	}
	if r := offline(e, "files.cancelOperation", map[string]any{"delegation_id": strings.Repeat("f", 64), "job_id": "job"}); r.Code != "operation-not-found" {
		t.Fatal("canceled under an unknown grant", r)
	}
	f.user.Username = "bob"
	if r := request(e, "files.cancelOperation", ref); r.Code != "operation-not-found" {
		t.Fatal("another owner canceled", r)
	}
	f.user.Username = "alice"
	for i := 0; i < 2; i++ {
		wantOperation(t, offline(e, "files.cancelOperation", ref), "canceled", "OPERATION_CANCELED", "operation-canceled-by-user")
	}
	// The original admission, arriving late or replayed, finds the tombstone.
	for i := 0; i < 2; i++ {
		wantOperation(t, offline(e, "files.moveFromStaging", moveParams(id, "job")), "canceled", "OPERATION_CANCELED", "operation-canceled-by-user")
	}
	wantOperation(t, offline(e, "files.operationStatus", ref), "canceled", "OPERATION_CANCELED", "operation-canceled-by-user")
	another, _ := destinationGrant(t, e, f)
	other := map[string]any{"delegation_id": another, "job_id": "job"}
	if r := offline(e, "files.cancelOperation", other); r.Code != "operation-not-found" {
		t.Fatal("tombstone answered another grant", r)
	}
	select {
	case <-w.copying:
		t.Fatal("canceled reference was copied")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCancelQueuedNeverCopies(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	w := newStubbornWriter()
	e.Writer = w
	startedCopy(t, e, w, id, "active")
	if r := offline(e, "files.moveFromStaging", moveParams(id, "queued")); !r.OK {
		t.Fatal(r)
	}
	ref := map[string]any{"delegation_id": id, "job_id": "queued"}
	for i := 0; i < 2; i++ {
		wantOperation(t, offline(e, "files.cancelOperation", ref), "canceled", "OPERATION_CANCELED", "operation-canceled-by-user")
	}
	w.let()
	waitState(t, e, "active", "committed")
	if op := waitState(t, e, "queued", "canceled"); op.Part != nil || op.PublishIntent {
		t.Fatal("canceled queued operation copied", op)
	}
}

func TestCancelRacingPublishReturnsCommitted(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	w := newStubbornWriter()
	w.publishing, w.finishPublish = make(chan struct{}), make(chan struct{})
	e.Writer = w
	startedCopy(t, e, w, id, "job")
	w.let()
	<-w.publishing
	reply := make(chan Response, 1)
	go func() {
		reply <- offline(e, "files.cancelOperation", map[string]any{"delegation_id": id, "job_id": "job"})
	}()
	select {
	case r := <-reply:
		t.Fatal("cancel answered inside the publish fence", r)
	case <-time.After(50 * time.Millisecond):
	}
	close(w.finishPublish)
	out := operationOf(t, <-reply)
	if out["state"] != "committed" || out["receipt"] == nil || out["code"] != nil {
		t.Fatal("cancel after publish must report committed", out)
	}
	waitState(t, e, "job", "committed")
}
