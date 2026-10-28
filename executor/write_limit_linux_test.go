//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The real writer checks the opened staging file's size before it binds the
// destination or starts any child, so this needs no root and no child binary.
func TestSubprocessWriterRefusesOverLimitStagingBeforeAnyChild(t *testing.T) {
	stage := t.TempDir()
	os.Chmod(stage, 0700)
	for name, size := range map[string]int64{"over": maxWriteBytes + 1, "at": maxWriteBytes} {
		src, err := os.Create(filepath.Join(stage, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = src.Truncate(size); err != nil {
			src.Close()
			t.Skip(err)
		}
		src.Close()
	}
	w := subprocessWriter{Staging: stage, Executable: "/nonexistent", PackageUID: os.Getuid(), PackageGID: os.Getgid()}
	missing := filepath.Join(t.TempDir(), "missing")
	_, err := w.Write(context.Background(), Subject{}, "over", missing, "f", missing)
	if !errors.Is(err, errWriteTooLarge) {
		t.Fatalf("over limit: %v", err)
	}
	// At the limit the size check passes; the missing destination fails later.
	_, err = w.Write(context.Background(), Subject{}, "at", missing, "f", missing)
	if err == nil || errors.Is(err, errWriteTooLarge) {
		t.Fatalf("at limit: %v", err)
	}
}
