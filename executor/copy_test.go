package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

type endlessZeros struct{ read int64 }

func (z *endlessZeros) Read(p []byte) (int, error) {
	clear(p)
	z.read += int64(len(p))
	return len(p), nil
}

// The move parent holds the child's control pipe open for the whole copy. If
// the parent dies, the pipe closes and the child must stop copying at once
// rather than fill a part file nobody can publish.
func TestCopyStopsWhenParentClosesControl(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	control := awaitControl(bufio.NewReader(r))
	src := &endlessZeros{}
	done := make(chan error, 1)
	go func() { done <- copyUnlessParentGone(io.Discard, src, 1<<50, control) }()
	time.Sleep(50 * time.Millisecond)
	w.Close()
	select {
	case err := <-done:
		if !errors.Is(err, errParentGone) {
			t.Fatal("want errParentGone, got", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("copy continued after the parent closed control")
	}
}

// The parent never writes during the copy; anything arriving there is a
// protocol violation and stops the copy too.
func TestCopyStopsOnUnexpectedControlInput(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	control := awaitControl(bufio.NewReader(r))
	done := make(chan error, 1)
	go func() { done <- copyUnlessParentGone(io.Discard, &endlessZeros{}, 1<<50, control) }()
	w.Write([]byte("P\n"))
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("copy accepted early control input")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("copy ignored control input")
	}
}

func TestCopyKeepsExactSizeAndLeavesDecisionForCaller(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	control := awaitControl(bufio.NewReader(r))
	var out bytes.Buffer
	if err = copyUnlessParentGone(&out, bytes.NewReader(make([]byte, 3<<20)), 3<<20, control); err != nil || out.Len() != 3<<20 {
		t.Fatal(out.Len(), err)
	}
	if copyUnlessParentGone(io.Discard, bytes.NewReader(make([]byte, 4)), 3, control) == nil {
		t.Fatal("excess bytes accepted")
	}
	w.Write([]byte("P\n"))
	if got := <-control; got.line != "P\n" || got.err != nil {
		t.Fatal("decision lost", got)
	}
	w.Close()
}
