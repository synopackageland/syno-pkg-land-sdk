package main

import (
	"context"
	"testing"
)

// Selecting the folder a person already granted, for example a retry after
// the client timed out while the first create completed, keeps that grant and
// its saves in flight.
func TestCreateForGrantedFolderReusesGrant(t *testing.T) {
	e, _, _ := setup(t)
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
	if again := longGrant(t, e); again != id {
		t.Fatal("same folder created another grant", again, id)
	}
	requireActive(t, e, id)
	close(w.release)
	waitState(t, e, "job", "committed")
}

// Another folder still replaces the person's previous choice.
func TestCreateForAnotherFolderReplacesGrant(t *testing.T) {
	e, f, _ := setup(t)
	id := longGrant(t, e)
	other, _ := destinationGrant(t, e, f)
	if other == id {
		t.Fatal("another folder reused the grant")
	}
	if d, err := e.Store.Get(id); err != nil || !d.Revoked {
		t.Fatal("previous choice kept", err)
	}
}
