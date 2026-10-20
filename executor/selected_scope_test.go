package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func selectedExecutor(t *testing.T) (*Executor, *fakeDSM) {
	e, f, _ := setup(t)
	e.Policy.Scope = installSelectedScope
	e.Policy.Lifetime = "until-revoked"
	e.Policy.Destinations = []string{"save"}
	f.user.Admin = true
	return e, f
}
func selectScope(e *Executor, scope string, peer int) Response {
	state, _ := e.Store.SelectedScope()
	b, _ := json.Marshal(map[string]any{"scope": scope, "actor": "alice", "expected_generation": state.Generation})
	return e.handlePeer(context.Background(), Request{Version: 1, Op: "delegate.setScope", Params: b}, peer, 3000)
}
func selectedGrant(t *testing.T, e *Executor, path string) Delegation {
	t.Helper()
	r := request(e, "delegate.create", map[string]any{"scope": installSelectedScope, "ops": []string{"files.moveFromStaging"}, "path": path, "destination": "save", "lifetime": "until-revoked"})
	if !r.OK {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r.Result)
	var d Delegation
	json.Unmarshal(b, &d)
	return d
}
func TestSelectedScopeOnlyPasswordConfirmedRootPeerMayChange(t *testing.T) {
	e, f := selectedExecutor(t)
	if r := selectScope(e, "shared:reports", 3000); r.OK {
		t.Fatal("package changed scope", r)
	}
	if r := selectScope(e, "shared:reports", 3001); r.OK {
		t.Fatal("other peer changed scope", r)
	}
	f.user.Admin = false
	if r := selectScope(e, "shared:reports", 0); r.OK {
		t.Fatal("nonadministrator actor", r)
	}
	f.user.Admin = true
	for _, params := range []string{`{"scope":"home","actor":"alice"}`, `{"scope":"home","actor":"alice","expected_generation":null}`, `{"scope":"home","actor":"alice","expected_generation":9007199254740992}`} {
		if r := e.handlePeer(context.Background(), Request{Version: 1, Op: "delegate.setScope", Params: json.RawMessage(params)}, 0, 3000); r.OK {
			t.Fatal("unbound root task accepted", r)
		}
	}
	for _, scope := range []string{"*", "shared:../escape", "shared:homes", "shared:home", "shared:", "home\n", "shared:a:b"} {
		if r := selectScope(e, scope, 0); r.OK {
			t.Fatal(scope, r)
		}
	}
	if r := selectScope(e, "shared:reports", 0); !r.OK {
		t.Fatal(r)
	}
	state, err := e.Store.SelectedScope()
	if err != nil || state.Scope != "shared:reports" || state.Generation != 1 {
		t.Fatal(state, err)
	}
	info, _ := os.Stat(filepath.Join(e.Store.Dir, "selected-scope.record"))
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	// No admin cookie/SynoToken can turn the ordinary package socket into root authority.
	if r := request(e, "delegate.setScope", map[string]string{"scope": "home"}); r.OK {
		t.Fatal(r)
	}
}
func TestSelectedScopeMissingCorruptAndChangeBackFailClosed(t *testing.T) {
	e, _ := selectedExecutor(t)
	p := map[string]any{"scope": installSelectedScope, "ops": []string{"files.moveFromStaging"}, "path": "/home", "destination": "save", "lifetime": "until-revoked"}
	if r := request(e, "delegate.create", p); r.OK {
		t.Fatal("unconfirmed scope", r)
	}
	if r := selectScope(e, "home", 0); !r.OK {
		t.Fatal(r)
	}
	d := selectedGrant(t, e, "/home")
	if d.Scope != "home" || d.ScopeGeneration != 1 {
		t.Fatal(d)
	}
	if r := selectScope(e, "home", 0); !r.OK {
		t.Fatal(r)
	}
	state, _ := e.Store.SelectedScope()
	if state.Generation != 1 {
		t.Fatal("unchanged scope invalidates", state)
	}
	if r := selectScope(e, "shared:reports", 0); !r.OK {
		t.Fatal(r)
	}
	if r := request(e, "delegate.create", p); r.Code != "SCOPE_DENIED" {
		t.Fatal("outside chosen share", r)
	}
	if r := selectScope(e, "home", 0); !r.OK {
		t.Fatal(r)
	}
	if _, code := e.validateMove(context.Background(), d); code == "" {
		t.Fatal("change-back revived old grant")
	}
	fresh := &Executor{Policy: e.Policy, DSM: e.DSM, Store: &Store{Dir: e.Store.Dir}, Now: e.Now}
	if _, code := fresh.validateMove(context.Background(), d); code == "" {
		t.Fatal("restart revived old grant")
	}
	d = selectedGrant(t, e, "/home")
	if d.ScopeGeneration != 3 {
		t.Fatal(d)
	}
	os.WriteFile(filepath.Join(e.Store.Dir, "selected-scope.record"), []byte(`{"schema":1,"scope":"home","generation":0}`), 0600)
	if r := request(e, "delegate.create", p); r.OK {
		t.Fatal("corrupt scope allowed", r)
	}
	if _, code := e.validateMove(context.Background(), d); code == "" {
		t.Fatal("corrupt state allowed move")
	}
}
func TestSelectedScopeChangeFencesActivePublish(t *testing.T) {
	e, _ := selectedExecutor(t)
	if r := selectScope(e, "home", 0); !r.OK {
		t.Fatal(r)
	}
	d := selectedGrant(t, e, "/home")
	w := &barrierMoveWriter{copying: make(chan struct{}), release: make(chan struct{}), published: make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := offline(e, "files.moveFromStaging", moveParams(d.ID, "scope-race")); !r.OK {
		t.Fatal(r)
	}
	<-w.copying
	if r := selectScope(e, "shared:reports", 0); !r.OK {
		t.Fatal(r)
	}
	close(w.release)
	if op := waitState(t, e, "scope-race", "canceled"); op.Reason != "scope-changed" {
		t.Fatal("missing scope cancellation reason", op)
	}
	select {
	case <-w.published:
		t.Fatal("published after scope change acknowledgement")
	default:
	}
}

func TestSelectedDestinationReplacesOnlyThisOwnerAndKey(t *testing.T) {
	e, f := selectedExecutor(t)
	if r := selectScope(e, "home", 0); !r.OK {
		t.Fatal(r)
	}
	if err := os.Mkdir(filepath.Join(f.root, "next"), 0700); err != nil {
		t.Fatal(err)
	}
	old := selectedGrant(t, e, "/home")
	fresh := selectedGrant(t, e, "/home/next")
	prior, err := e.Store.Get(old.ID)
	if err != nil || !prior.Revoked {
		t.Fatal("prior destination still active", prior, err)
	}
	current, err := e.Store.Get(fresh.ID)
	if err != nil || current.Revoked {
		t.Fatal(current, err)
	}
}

func TestSelectedScopeTaskCannotReplayAfterAnotherConfirmation(t *testing.T) {
	e, _ := selectedExecutor(t)
	b, _ := json.Marshal(map[string]any{"scope": "home", "actor": "alice", "expected_generation": uint64(0)})
	old := Request{Version: 1, Op: "delegate.setScope", Params: b}
	if r := e.handlePeer(context.Background(), old, 0, 3000); !r.OK {
		t.Fatal(r)
	}
	if r := selectScope(e, "shared:reports", 0); !r.OK {
		t.Fatal(r)
	}
	if r := e.handlePeer(context.Background(), old, 0, 3000); r.Code != "SCOPE_APPROVAL_STALE" {
		t.Fatal("old root task widened the newly confirmed scope", r)
	}
	state, err := e.Store.SelectedScope()
	if err != nil || state.Scope != "shared:reports" || state.Generation != 2 {
		t.Fatal(state, err)
	}
}
