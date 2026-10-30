//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRawActiveStagingOutlivesOrphanRetention(t *testing.T) {
	w := subprocessWriter{Staging: filepath.Join(t.TempDir(), "executor-staging"), PackageUID: os.Getuid(), PackageGID: os.Getgid()}
	free, err := w.Available()
	if err != nil || free < 0 {
		t.Fatal("cannot measure destination filesystem", free, err)
	}
	f, name, err := w.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("active"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	path := filepath.Join(w.Staging, name)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if used, err := w.Used(); err != nil || used != 6 {
		t.Fatal("scanner reclaimed an active two-hour download", used, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("active staging unlinked", err)
	}
	f.Close()
	if used, err := w.Used(); err != nil || used != 0 {
		t.Fatal("abandoned staging was not reclaimed", used, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("finished orphan remains", err)
	}
	f, name, err = w.Create()
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(w.Staging, name)
	if _, err := f.WriteString("ready"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	out := &rootOutput{file: f, name: name, n: 5, staging: w}
	if _, _, err := out.keep(); err != nil {
		t.Fatal(err)
	}
	if used, err := w.Used(); err != nil || used != 5 {
		t.Fatal("just-completed idle output lost its retention window", used, err)
	}
	w.Remove(name)
}
