package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// addressRecorder records the client address each App Privileges check saw.
type addressRecorder struct {
	*fakeDSM
	mu    sync.Mutex
	addrs map[string]string
}

func (f *addressRecorder) Allow(ctx context.Context, _ Subject, app, _ string) error {
	addr, _ := clientAddress(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addrs[app] = addr
	return nil
}
func recordAddresses(e *Executor, f *fakeDSM) *addressRecorder {
	r := &addressRecorder{fakeDSM: f, addrs: map[string]string{}}
	e.DSM = r
	return r
}
func (f *addressRecorder) seen(app string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	addr, ok := f.addrs[app]
	return addr, ok
}

func TestCallerAddressReachesBothAppPrivilegeChecks(t *testing.T) {
	e, f, _ := setup(t)
	r := recordAddresses(e, f)
	p, _ := json.Marshal(map[string]any{"job_id": "j", "ops": []string{"files.list"}, "scope": "home", "path": "/home", "ttl_seconds": 10})
	req := Request{Version: 1, Op: "delegate.create", Auth: Auth{Cookie: "session", SynoToken: "token", RemoteAddr: "192.0.2.9"}, Params: p}
	if out := e.Handle(context.Background(), req); !out.OK {
		t.Fatal(out)
	}
	for _, app := range []string{"app", "SYNO.SDS.App.FileStation3.Instance"} {
		if addr, _ := r.seen(app); addr != "192.0.2.9" {
			t.Fatalf("%s checked from %q", app, addr)
		}
	}
}

// A caller without an address gets none, never the NAS's own: Allow refuses.
func TestCallerWithoutAddressIsNotCheckedAsTheNAS(t *testing.T) {
	e, f, _ := setup(t)
	r := recordAddresses(e, f)
	if out := request(e, "delegate.create", map[string]any{"job_id": "j", "ops": []string{"files.list"}, "scope": "home", "path": "/home", "ttl_seconds": 10}); !out.OK {
		t.Fatal(out)
	}
	if addr, ok := r.seen("app"); !ok || addr != "" {
		t.Fatalf("checked from %q", addr)
	}
}

// Work no browser asked for, and a grant that recorded no browser, is
// checked as coming from the NAS itself.
func TestWorkWithoutABrowserIsCheckedAsTheNAS(t *testing.T) {
	e, f, _ := setup(t)
	id := create(t, e)
	r := recordAddresses(e, f)
	if out := offline(e, "files.list", map[string]any{"delegation_id": id, "job_id": "job1", "path": "/home"}); !out.OK {
		t.Fatal(out)
	}
	if addr, _ := r.seen("app"); addr != localAddress {
		t.Fatalf("grant checked from %q", addr)
	}

	e, f = selectedExecutor(t)
	r = recordAddresses(e, f)
	if out := selectScope(e, "home", 0); !out.OK {
		t.Fatal(out)
	}
	if addr, _ := r.seen("app"); addr != localAddress {
		t.Fatalf("setScope checked from %q", addr)
	}

	e, _, _ = setup(t)
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if addr, ok := clientAddress(e.async.ctx); !ok || addr != localAddress {
		t.Fatalf("background operations checked from %q", addr)
	}
}

func TestAuthCarriesTheRemoteAddress(t *testing.T) {
	var req Request
	if err := strictJSON([]byte(`{"v":1,"op":"delegate.list","auth":{"dsm_cookie":"a","dsm_synotoken":"t","remote_addr":"2001:db8::1"},"params":{}}`), &req); err != nil || req.Auth.RemoteAddr != "2001:db8::1" {
		t.Fatal(req.Auth, err)
	}
}

const browserAddr = "192.0.2.10"

func requestFrom(e *Executor, addr, op string, params any) Response {
	b, _ := json.Marshal(params)
	return e.Handle(context.Background(), Request{Version: 1, Op: op, Auth: Auth{Cookie: "session", SynoToken: "token", RemoteAddr: addr}, Params: b})
}

// longGrantFrom creates an until-revoked grant from the browser at addr.
func longGrantFrom(t *testing.T, e *Executor, addr string) string {
	t.Helper()
	e.Policy.Destinations = []string{"save"}
	e.Policy.Lifetime = "until-revoked"
	r := requestFrom(e, addr, "delegate.create", map[string]any{"ops": []string{"files.moveFromStaging"}, "scope": "home", "path": "/home", "destination": "save", "lifetime": "until-revoked"})
	if !r.OK {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r.Result)
	var d Delegation
	json.Unmarshal(b, &d)
	return d.ID
}

// waitCopying fails, instead of hanging, when a save never starts copying.
func waitCopying(t *testing.T, w *barrierMoveWriter) {
	t.Helper()
	select {
	case <-w.copying:
	case <-time.After(2 * time.Second):
		t.Fatal("the save did not start copying")
	}
}

func allowOnly(f *fakeDSM, addr string) {
	f.apiMu.Lock()
	f.allowedAddr = addr
	f.apiMu.Unlock()
}

// A rule limited to the browser's address allows the background save the
// browser's grant started: it is checked from the address that created the grant.
func TestBackgroundSaveIsCheckedFromTheGrantsBrowserAddress(t *testing.T) {
	e, f, _ := setup(t)
	allowOnly(f, browserAddr)
	id := longGrantFrom(t, e, browserAddr)
	w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := offline(e, "files.moveFromStaging", moveParams(id, "job")); !r.OK {
		t.Fatal(r)
	}
	waitCopying(t, w)
	close(w.release)
	waitState(t, e, "job", "committed")
}

// A save submitted with a session is checked from that request's address,
// which the operation keeps for its background revalidation.
func TestAuthenticatedSaveIsCheckedFromItsOwnAddress(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrantFrom(t, e, browserAddr)
	allowOnly(f, "192.0.2.20")
	w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := requestFrom(e, "192.0.2.20", "files.moveFromStaging", moveParams(id, "job")); !r.OK {
		t.Fatal(r)
	}
	waitCopying(t, w)
	close(w.release)
	waitState(t, e, "job", "committed")
}

// Application Privileges an administrator can change refuse the save only;
// the grant stays, so a save after the rule allows it again succeeds.
func TestAdmissionAppDeniedKeepsGrant(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrantFrom(t, e, browserAddr)
	e.Writer = &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	allowOnly(f, "198.51.100.1")
	if r := offline(e, "files.moveFromStaging", moveParams(id, "denied")); r.Code != "APP_DENIED" {
		t.Fatal(r)
	}
	requireActive(t, e, id)
	allowOnly(f, browserAddr)
	if r := offline(e, "files.moveFromStaging", moveParams(id, "retry")); !r.OK {
		t.Fatal("retry after the rule allows the address again", r)
	}
}

// Publish revalidates; an App Privileges denial there interrupts this save only.
func TestPublishAppDeniedKeepsGrant(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrantFrom(t, e, browserAddr)
	w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := offline(e, "files.moveFromStaging", moveParams(id, "job")); !r.OK {
		t.Fatal(r)
	}
	waitCopying(t, w)
	allowOnly(f, "198.51.100.1")
	close(w.release)
	if op := waitState(t, e, "job", "interrupted"); op.Code != "APP_DENIED" {
		t.Fatal(op)
	}
	requireActive(t, e, id)
}

// A job's grant-based file op without a session is checked from the browser
// address that created the job's grant.
func TestJobFileOpIsCheckedFromTheGrantsBrowserAddress(t *testing.T) {
	e, f, _ := setup(t)
	allowOnly(f, browserAddr)
	r := requestFrom(e, browserAddr, "delegate.create", map[string]any{"job_id": "job1", "ops": []string{"files.list"}, "scope": "home", "path": "/home", "ttl_seconds": 60})
	if !r.OK {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r.Result)
	var d Delegation
	json.Unmarshal(b, &d)
	if r = offline(e, "files.list", map[string]any{"delegation_id": d.ID, "job_id": "job1", "path": "/home"}); !r.OK {
		t.Fatal(r)
	}
}

// Choosing the same folder again from another address keeps the grant and
// checks its later background work from the new address.
func TestRecreatingAGrantRecordsTheNewAddress(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrantFrom(t, e, browserAddr)
	if again := longGrantFrom(t, e, "192.0.2.20"); again != id {
		t.Fatal("re-create replaced the grant")
	}
	allowOnly(f, "192.0.2.20")
	e.Writer = &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := offline(e, "files.moveFromStaging", moveParams(id, "job")); !r.OK {
		t.Fatal(r)
	}
}
