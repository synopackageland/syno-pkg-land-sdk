package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLongGrantExplicitOptInAndPersistence(t *testing.T) {
	e, f, _ := setup(t)
	p := map[string]any{"ops": []string{"files.moveFromStaging"}, "scope": "home", "path": "/home", "destination": "save", "lifetime": "until-revoked"}
	if r := request(e, "delegate.create", p); r.OK {
		t.Fatal("unopted policy created long grant")
	}
	e.Policy.Destinations = []string{"save"}
	e.Policy.Lifetime = "until-revoked"
	f.user.Admin = true
	r := request(e, "delegate.create", p)
	if !r.OK {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r.Result)
	var d Delegation
	json.Unmarshal(b, &d)
	persisted, err := (&Store{Dir: e.Store.Dir}).Get(d.ID)
	if err != nil || persisted.Lifetime != "until-revoked" || !persisted.Expires.IsZero() || persisted.Subject.UID != 1200 {
		t.Fatal(persisted, err)
	}
	e.Now = func() time.Time { return time.Unix(9999999999, 0) }
	if r := request(e, "delegate.revoke", map[string]any{"delegation_id": d.ID}); !r.OK {
		t.Fatal(r)
	}
}
func TestAdminDisableCoversLegacyAndPersists(t *testing.T) {
	e, f, w := setup(t)
	id := create(t, e)
	if r := request(e, "delegate.disable", map[string]any{}); r.Code != "ADMIN_REQUIRED" {
		t.Fatal(r)
	}
	f.user.Admin = true
	if r := request(e, "delegate.disable", map[string]any{}); !r.OK {
		t.Fatal(r)
	}
	fresh := &Executor{Policy: e.Policy, DSM: e.DSM, Writer: w, Store: &Store{Dir: e.Store.Dir}, Now: e.Now}
	if r := request(fresh, "delegate.create", map[string]any{"job_id": "new", "ops": []string{"files.write"}, "scope": "home", "path": "/home", "ttl_seconds": 60}); r.Code != "disabled" {
		t.Fatal(r)
	}
	if r := request(fresh, "files.write", map[string]any{"delegation_id": id, "job_id": "job1", "staging": "source", "dest_dir": "/home", "name": "x"}); r.Code != "disabled" || w.calls != 0 {
		t.Fatal(r)
	}
	if r := request(fresh, "delegate.enable", map[string]any{}); !r.OK {
		t.Fatal(r)
	}
	if r := request(fresh, "files.write", map[string]any{"delegation_id": id, "job_id": "job1", "staging": "source", "dest_dir": "/home", "name": "x"}); r.Code != "DELEGATION_DENIED" {
		t.Fatal("enable revived grant", r)
	}
}
func TestLongGrantNoSessionlessRevoke(t *testing.T) {
	e, _, _ := setup(t)
	e.Policy.Destinations = []string{"save"}
	e.Policy.Lifetime = "until-revoked"
	r := request(e, "delegate.create", map[string]any{"ops": []string{"files.moveFromStaging"}, "scope": "home", "path": "/home", "destination": "save", "lifetime": "until-revoked"})
	if !r.OK {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r.Result)
	var d Delegation
	json.Unmarshal(b, &d)
	r = e.Handle(context.Background(), Request{Version: 1, Op: "delegate.revoke", Params: json.RawMessage(`{"delegation_id":"` + d.ID + `","job_id":"job1"}`)})
	if r.Code != "DELEGATION_DENIED" {
		t.Fatal(r)
	}
}

// Two-phase fake blocks during bulk work but calls the REAL protected fence.
type barrierMoveWriter struct {
	copying, release chan struct{}
	published        chan struct{}
}

func (w *barrierMoveWriter) Write(context.Context, Subject, string, string, string, string) (MoveReceipt, error) {
	return MoveReceipt{}, nil
}
func (w *barrierMoveWriter) Move(ctx context.Context, s Subject, op Operation, d Delegation, guard func(func() (MoveReceipt, error)) (MoveReceipt, error)) (MoveReceipt, error) {
	close(w.copying)
	select {
	case <-ctx.Done():
		return MoveReceipt{}, ctx.Err()
	case <-w.release:
	}
	return guard(func() (MoveReceipt, error) { close(w.published); return MoveReceipt{Path: d.Path + "/" + op.Name}, nil })
}
func longGrant(t *testing.T, e *Executor) string {
	t.Helper()
	e.Policy.Destinations = []string{"save"}
	e.Policy.Lifetime = "until-revoked"
	r := request(e, "delegate.create", map[string]any{"ops": []string{"files.moveFromStaging"}, "scope": "home", "path": "/home", "destination": "save", "lifetime": "until-revoked"})
	if !r.OK {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r.Result)
	var d Delegation
	json.Unmarshal(b, &d)
	return d.ID
}
func offline(e *Executor, op string, p any) Response {
	b, _ := json.Marshal(p)
	return e.Handle(context.Background(), Request{Version: 1, Op: op, Params: b})
}
func moveParams(id, job string) map[string]any {
	return map[string]any{"delegation_id": id, "job_id": job, "staging": "result", "name": "out.csv", "size": 123}
}
func waitState(t *testing.T, e *Executor, job, state string) Operation {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.commitMu.Lock()
		op, err := e.Store.GetOperation(job)
		e.commitMu.Unlock()
		if err == nil && op.State == state {
			return op
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("missing state", state)
	return Operation{}
}
func TestRevokeDisableCancelNeverPublishAfterAcknowledgement(t *testing.T) {
	for _, action := range []string{"delegate.revoke", "delegate.disable", "files.cancelOperation"} {
		t.Run(action, func(t *testing.T) {
			e, f, _ := setup(t)
			id := longGrant(t, e)
			w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
			e.Writer = w
			if err := e.startOperations(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer e.stopOperations()
			if r := offline(e, "files.moveFromStaging", moveParams(id, "job")); !r.OK {
				t.Fatal(r)
			}
			select {
			case <-w.copying:
			case <-time.After(time.Second):
				t.Fatal("copy did not start")
			}
			if action == "delegate.disable" {
				f.user.Admin = true
			}
			r := request(e, action, map[string]any{"delegation_id": id, "job_id": "job"})
			if action == "delegate.disable" {
				r = request(e, action, map[string]any{})
			}
			if !r.OK {
				t.Fatal(r)
			}
			close(w.release)
			waitState(t, e, "job", "canceled")
			select {
			case <-w.published:
				t.Fatal("published after revoke/disable/cancel")
			default:
			}
		})
	}
}
func TestCommittedIdempotencyReceiptAndOwnerIsolation(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	p := moveParams(id, "job")
	if r := offline(e, "files.moveFromStaging", p); !r.OK {
		t.Fatal(r)
	}
	<-w.copying
	close(w.release)
	waitState(t, e, "job", "committed")
	r := offline(e, "files.operationStatus", map[string]any{"delegation_id": id, "job_id": "job"})
	b, _ := json.Marshal(r.Result)
	if !r.OK || !strings.Contains(string(b), `"receipt"`) {
		t.Fatal(r)
	}
	if r = offline(e, "files.moveFromStaging", p); !r.OK {
		t.Fatal(r)
	}
	p["name"] = "other"
	if r = offline(e, "files.moveFromStaging", p); r.Code != "operation-conflict" {
		t.Fatal(r)
	}
	f.user.Username = "bob"
	if r = request(e, "files.operationStatus", map[string]any{"delegation_id": id, "job_id": "job"}); r.Code != "operation-not-found" {
		t.Fatal(r)
	}
}
func TestCrashRecoveryNeverReplaysCopyOrUncertain(t *testing.T) {
	for _, intent := range []bool{false, true} {
		t.Run(fmt.Sprint(intent), func(t *testing.T) {
			e, _, _ := setup(t)
			id := longGrant(t, e)
			op := Operation{Version: 1, Package: e.Policy.Package, ID: id, Job: "job", State: "copying", PublishIntent: intent, Staging: "result", Name: "out.csv", Size: 123}
			if e.Store.PutOperation(op) != nil {
				t.Fatal("put")
			}
			w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
			e.Writer = w
			if err := e.startOperations(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer e.stopOperations()
			want := "interrupted"
			if intent {
				want = "uncertain"
			}
			waitState(t, e, "job", want)
			if r := offline(e, "files.moveFromStaging", moveParams(id, "job")); !r.OK {
				t.Fatal("same job lost", r)
			}
			select {
			case <-w.copying:
				t.Fatal("crash operation replayed")
			default:
			}
		})
	}
}

func TestLongGrantRejectsInScopeSymlinkAndUnknownDestination(t *testing.T) {
	e, f, _ := setup(t)
	e.Policy.Destinations = []string{"save"}
	e.Policy.Lifetime = "until-revoked"
	os.Mkdir(filepath.Join(f.root, "real"), 0700)
	os.Symlink(filepath.Join(f.root, "real"), filepath.Join(f.root, "alias"))
	for _, p := range []map[string]any{
		{"ops": []string{"files.moveFromStaging"}, "scope": "home", "path": "/home/alias", "destination": "save", "lifetime": "until-revoked"},
		{"ops": []string{"files.moveFromStaging"}, "scope": "home", "path": "/home", "destination": "unknown", "lifetime": "until-revoked"},
	} {
		if r := request(e, "delegate.create", p); r.OK {
			t.Fatal("invalid destination granted")
		}
	}
}
func TestLongGrantRevokeIsOwnerBoundEvenForAdmin(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	f.user.Username = "bob"
	f.user.Admin = true
	if r := request(e, "delegate.revoke", map[string]any{"delegation_id": id}); r.Code != "DELEGATION_DENIED" {
		t.Fatal(r)
	}
}
func TestInvalidatedGrantRequiresReselection(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	e.Writer = &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	f.accountErr = errors.New("disabled")
	if r := offline(e, "files.moveFromStaging", moveParams(id, "job")); r.Code != "INELIGIBLE" {
		t.Fatal(r)
	}
	f.accountErr = nil
	if r := offline(e, "files.moveFromStaging", moveParams(id, "job2")); r.Code != "DELEGATION_DENIED" {
		t.Fatal("revived invalid grant", r)
	}
}

type cleanupFailureWriter struct{}

func (cleanupFailureWriter) Write(context.Context, Subject, string, string, string, string) (MoveReceipt, error) {
	return MoveReceipt{}, nil
}
func (cleanupFailureWriter) Move(_ context.Context, _ Subject, _ Operation, _ Delegation, guard func(func() (MoveReceipt, error)) (MoveReceipt, error)) (MoveReceipt, error) {
	return guard(func() (MoveReceipt, error) { return MoveReceipt{CleanupPending: true}, nil })
}
func TestCommittedNeverDowngradesOnCleanupFailure(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	e.Writer = cleanupFailureWriter{}
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := offline(e, "files.moveFromStaging", moveParams(id, "job")); !r.OK {
		t.Fatal(r)
	}
	op := waitState(t, e, "job", "committed")
	if op.Code != "CLEANUP_PENDING" || op.Path != "/home/out.csv" {
		t.Fatal(op)
	}
}
func TestQueuedRestartsWithoutCredentialsAndUnknownRecordFailsClosed(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	d, _ := e.Store.Get(id)
	op := Operation{Version: 1, Package: e.Policy.Package, ID: id, Job: "job", State: "queued", Subject: d.Subject, Staging: "result", Name: "out.csv", Size: 123}
	if e.Store.PutOperation(op) != nil {
		t.Fatal("put")
	}
	w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.copying:
	case <-time.After(time.Second):
		t.Fatal("queued not recovered")
	}
	close(w.release)
	waitState(t, e, "job", "committed")
	e.stopOperations()
	path := filepath.Join(e.Store.Dir, "operation-"+recordKey(op.Job)+".record")
	os.WriteFile(path, []byte(`{"v":99,"job_id":"job"}`), 0600)
	if err := e.startOperations(context.Background()); err == nil {
		e.stopOperations()
		t.Fatal("unknown schema started")
	}
}
func TestLegacyLeaseDrainsWithoutHoldingCommitLock(t *testing.T) {
	e, _, _ := setup(t)
	id := create(t, e)
	d, _ := e.Store.Get(id)
	done, code := e.beginLegacy(d)
	if code != "" {
		t.Fatal(code)
	}
	revoked := make(chan Response, 1)
	go func() { revoked <- request(e, "delegate.revoke", map[string]any{"delegation_id": id}) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		fresh, _ := e.Store.Get(id)
		if fresh.Revoked {
			break
		}
		time.Sleep(time.Millisecond)
	}
	lock := make(chan struct{})
	go func() { e.commitMu.Lock(); e.commitMu.Unlock(); close(lock) }()
	select {
	case <-lock:
	case <-time.After(time.Second):
		t.Fatal("drain held commit lock")
	}
	select {
	case <-revoked:
		t.Fatal("revoke acknowledged before legacy drain")
	default:
	}
	done()
	select {
	case r := <-revoked:
		if !r.OK {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("revoke did not finish")
	}
}

func TestRedundantEnablePreservesNewGrant(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	f.user.Admin = true
	if r := request(e, "delegate.enable", map[string]any{}); !r.OK {
		t.Fatal(r)
	}
	d, err := e.Store.Get(id)
	if err != nil || d.Revoked {
		t.Fatal("redundant enable revoked grant", d, err)
	}
}
func TestAuditOwnerFilteringAndNoCredentials(t *testing.T) {
	e, f, _ := setup(t)
	_ = longGrant(t, e)
	f.user.Username = "bob"
	r := request(e, "delegate.audit", map[string]any{})
	b, _ := json.Marshal(r.Result)
	if !r.OK || strings.Contains(string(b), "alice") {
		t.Fatal("audit owner leak", r)
	}
	f.user.Admin = true
	r = request(e, "delegate.audit", map[string]any{})
	b, _ = json.Marshal(r.Result)
	if !r.OK || !strings.Contains(string(b), "alice") || strings.Contains(string(b), "session") || strings.Contains(string(b), "token") {
		t.Fatal("admin audit", r)
	}
}

func TestQueueAndByteLimitsFailClosed(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := offline(e, "files.moveFromStaging", moveParams(id, "active")); !r.OK {
		t.Fatal(r)
	}
	<-w.copying
	for i := 0; i < maxQueued; i++ {
		if r := offline(e, "files.moveFromStaging", moveParams(id, fmt.Sprintf("queued-%d", i))); !r.OK {
			t.Fatal(i, r)
		}
	}
	if r := offline(e, "files.moveFromStaging", moveParams(id, "overflow")); r.Code != "queue-full" {
		t.Fatal(r)
	}
	p := moveParams(id, "oversize")
	p["size"] = maxMoveBytes + 1
	if r := offline(e, "files.moveFromStaging", p); r.Code != "invalid-size" {
		t.Fatal(r)
	}
	p["size"] = -1
	if r := offline(e, "files.moveFromStaging", p); r.Code != "invalid-size" {
		t.Fatal(r)
	}
}

type canceledDSM struct{ *fakeDSM }

func (d canceledDSM) Account(ctx context.Context, name string) (Subject, error) {
	if err := ctx.Err(); err != nil {
		return Subject{}, err
	}
	return d.fakeDSM.Account(ctx, name)
}
func TestLifecycleCancellationDoesNotRevokeHealthyGrant(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	e.DSM = canceledDSM{f}
	e.Writer = cleanupFailureWriter{}
	d, _ := e.Store.Get(id)
	op := Operation{Version: 1, Package: e.Policy.Package, ID: id, Job: "job", Subject: d.Subject, State: "copying"}
	if e.Store.PutOperation(op) != nil {
		t.Fatal("put")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.performMove(ctx, &operationRuntime{ctx: ctx}, op)
	d, err := e.Store.Get(id)
	if err != nil || d.Revoked {
		t.Fatal("stop canceled permanent authority", d, err)
	}
}

func TestLegacyAndNewBulkCopyShareOneActiveSlot(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	legacy := create(t, e)
	w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := offline(e, "files.moveFromStaging", moveParams(id, "active")); !r.OK {
		t.Fatal(r)
	}
	<-w.copying
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	params, _ := json.Marshal(map[string]any{"delegation_id": legacy, "job_id": "job1", "staging": "source", "dest_dir": "/home", "name": "other"})
	r := e.Handle(ctx, Request{Version: 1, Op: "files.write", Params: params})
	if r.OK || r.Code != "EXECUTOR_PAUSED" {
		t.Fatal("legacy copy bypassed package active limit", r)
	}
}

type publishedPartWriter struct {
	*barrierMoveWriter
	published bool
	cleaned   int
}

func (w *publishedPartWriter) PublishedPart(_ context.Context, op Operation) (string, bool) {
	return op.Name, w.published
}
func (w *publishedPartWriter) CleanupRecorded(context.Context, Subject, partMetadata) error {
	w.cleaned++
	return nil
}

// A crash between publish intent and the committed record leaves "uncertain".
// Restart settles it from the recorded part inode instead of copying again.
func TestRestartSettlesUncertainFromRecordedPart(t *testing.T) {
	for _, published := range []bool{true, false} {
		t.Run(fmt.Sprint(published), func(t *testing.T) {
			e, _, _ := setup(t)
			id := longGrant(t, e)
			part := &partMetadata{Temporary: ".synopkgland-job-0123456789abcdef0123456789abcdef.part", Directory: directoryIdentity{1, 2}, Part: directoryIdentity{1, 3}, Destination: "/volume1/home"}
			op := Operation{Version: 1, Package: e.Policy.Package, ID: id, Job: "job", Subject: Subject{Username: "alice", UID: 1200}, State: "copying", PublishIntent: true, Staging: "result", Name: "out.csv", Size: 123, Part: part}
			if e.Store.PutOperation(op) != nil {
				t.Fatal("put")
			}
			w := &publishedPartWriter{barrierMoveWriter: &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}, published: published}
			e.Writer = w
			if err := e.startOperations(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer e.stopOperations()
			want := "uncertain"
			if published {
				want = "committed"
			}
			waitState(t, e, "job", want)
			got, _ := e.Store.GetOperation("job")
			if published && (got.Path != "/home/out.csv" || got.Code != "") {
				t.Fatal(got)
			}
			select {
			case <-w.copying:
				t.Fatal("uncertain operation replayed")
			default:
			}
		})
	}
}
