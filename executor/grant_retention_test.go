package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// destinationGrant creates an until-revoked grant for /home/dest.
func destinationGrant(t *testing.T, e *Executor, f *fakeDSM) (string, string) {
	t.Helper()
	dir := filepath.Join(f.root, "dest")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	e.Policy.Destinations = []string{"save"}
	e.Policy.Lifetime = "until-revoked"
	r := request(e, "delegate.create", map[string]any{"ops": []string{"files.moveFromStaging"}, "scope": "home", "path": "/home/dest", "destination": "save", "lifetime": "until-revoked"})
	if !r.OK {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r.Result)
	var d Delegation
	json.Unmarshal(b, &d)
	return d.ID, dir
}

func requireActive(t *testing.T, e *Executor, id string) {
	t.Helper()
	d, err := e.Store.Get(id)
	if err != nil || d.Revoked {
		t.Fatal("grant revoked", d.Revoked, err)
	}
}

// An ACL the user can undo refuses the save; the grant waits for the fix.
func TestAdmissionPermissionDeniedKeepsGrant(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	e.Writer = &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	f.permissionErr = errors.New("denied")
	if r := offline(e, "files.moveFromStaging", moveParams(id, "denied")); r.Code != "PERMISSION_DENIED" {
		t.Fatal(r)
	}
	requireActive(t, e, id)
	f.permissionErr = nil
	if r := offline(e, "files.moveFromStaging", moveParams(id, "retry")); !r.OK {
		t.Fatal("retry after restoring the permission", r)
	}
}

// Publish revalidates; an ACL denial there interrupts this save only.
func TestPublishPermissionDeniedKeepsGrant(t *testing.T) {
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
	<-w.copying
	f.apiMu.Lock()
	f.permissionErr = errors.New("denied")
	f.apiMu.Unlock()
	close(w.release)
	if op := waitState(t, e, "job", "interrupted"); op.Code != "PERMISSION_DENIED" {
		t.Fatal(op)
	}
	requireActive(t, e, id)
}

// The revalidation during a long copy treats an ACL denial like publish does.
func TestCopyRevalidationPermissionDeniedKeepsGrant(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	interval := moveRevalidationInterval
	moveRevalidationInterval = time.Millisecond
	defer func() { moveRevalidationInterval = interval }()
	w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if r := offline(e, "files.moveFromStaging", moveParams(id, "job")); !r.OK {
		t.Fatal(r)
	}
	<-w.copying
	f.apiMu.Lock()
	f.permissionErr = errors.New("denied")
	f.apiMu.Unlock()
	if op := waitState(t, e, "job", "canceled"); op.Reason != "validation-denied" {
		t.Fatal(op)
	}
	requireActive(t, e, id)
}

// A destination folder moved away is not-found; moving it back saves again.
func TestMissingDestinationIsNotFoundAndKeepsGrant(t *testing.T) {
	e, f, _ := setup(t)
	id, dir := destinationGrant(t, e, f)
	e.Writer = &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	if err := os.Rename(dir, dir+".moved"); err != nil {
		t.Fatal(err)
	}
	if r := offline(e, "files.moveFromStaging", moveParams(id, "missing")); r.Code != "not-found" {
		t.Fatal(r)
	}
	requireActive(t, e, id)
	if err := os.Rename(dir+".moved", dir); err != nil {
		t.Fatal(err)
	}
	if r := offline(e, "files.moveFromStaging", moveParams(id, "restored")); !r.OK {
		t.Fatal(r)
	}
}

// A different folder at the granted path can never match the grant again.
func TestReplacedDestinationRevokesGrant(t *testing.T) {
	e, f, _ := setup(t)
	id, dir := destinationGrant(t, e, f)
	e.Writer = &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	if err := os.Rename(dir, dir+".moved"); err != nil || os.Mkdir(dir, 0700) != nil {
		t.Fatal(err)
	}
	if r := offline(e, "files.moveFromStaging", moveParams(id, "replaced")); r.Code != "SCOPE_DENIED" {
		t.Fatal(r)
	}
	if fresh, err := e.Store.Get(id); err != nil || !fresh.Revoked {
		t.Fatal("replaced folder kept the grant", err)
	}
}

// Without a purge the revoked grant is still stored, so the producer's cancel
// of a refused admission records a canceled tombstone.
func TestCancelOfRefusedMoveUnderRevokedGrantCompletes(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	e.Writer = &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	f.privilegeErr = errors.New("deny")
	if r := offline(e, "files.moveFromStaging", moveParams(id, "refused")); r.Code != "APP_DENIED" {
		t.Fatal(r)
	}
	r := offline(e, "files.cancelOperation", map[string]any{"delegation_id": id, "job_id": "refused"})
	b, _ := json.Marshal(r.Result)
	if !r.OK || !strings.Contains(string(b), `"state":"canceled"`) {
		t.Fatal(r)
	}
}
