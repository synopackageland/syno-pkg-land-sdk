package main

import (
	"context"
	"testing"
)

type limitWriter struct{ err error }

func (w limitWriter) Write(context.Context, Subject, string, string, string, string) (MoveReceipt, error) {
	return MoveReceipt{}, w.err
}

func TestWriteLimitIsSeparateFromTheReadLimit(t *testing.T) {
	if maxWriteBytes != 100<<20 || maxReadBytes != 64<<20 || maxMoveBytes != 16<<30 {
		t.Fatal(maxWriteBytes, maxReadBytes, maxMoveBytes)
	}
}

func TestCheckWriteSizeBoundary(t *testing.T) {
	for size, want := range map[int64]error{0: nil, 1: nil, maxReadBytes + 1: nil, maxWriteBytes - 1: nil, maxWriteBytes: nil, maxWriteBytes + 1: errWriteTooLarge} {
		if got := checkWriteSize(size); got != want {
			t.Fatalf("size %d: %v, want %v", size, got, want)
		}
	}
}

func TestFilesWriteOverLimitIsExplicitlyRefused(t *testing.T) {
	e, _, _ := setup(t)
	id := create(t, e)
	p := map[string]any{"delegation_id": id, "job_id": "job1", "staging": "a", "dest_dir": "/home", "name": "new"}
	e.Writer = limitWriter{errWriteTooLarge}
	if r := request(e, "files.write", p); r.OK || r.Code != "limit-exceeded" {
		t.Fatalf("%+v", r)
	}
	// Any other writer failure is still the generic write refusal.
	e.Writer = limitWriter{context.DeadlineExceeded}
	if r := request(e, "files.write", p); r.OK || r.Code != "WRITE_FAILED" {
		t.Fatalf("%+v", r)
	}
	e.Writer = limitWriter{}
	if r := request(e, "files.write", p); !r.OK {
		t.Fatalf("%+v", r)
	}
}
