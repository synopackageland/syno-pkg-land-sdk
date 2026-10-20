//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestReviewCanceledWriteDoesNotLeavePartialDestination(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires disposable local Linux root runner")
	}
	base, err := os.MkdirTemp("/tmp", "p2review-write-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	os.Chmod(base, 0711)
	stage, dest := filepath.Join(base, "stage"), filepath.Join(base, "dest")
	os.Mkdir(stage, 0700)
	os.Mkdir(dest, 0700)
	s := Subject{UID: 1200, GID: 100, Groups: []int{100}}
	os.Chown(dest, s.UID, s.GID)
	src, err := os.Create(filepath.Join(stage, "source"))
	if err != nil {
		t.Fatal(err)
	}
	if err = src.Truncate(8 << 30); err != nil {
		src.Close()
		t.Skip(err)
	}
	src.Close()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(base, "child")
	body := "#!/bin/sh\nexport W3_MOVE_CHILD=\"$1\"\nexec " + strconv.Quote(binary) + " -test.run=^TestMoveChild$ 2>" + strconv.Quote(filepath.Join(base, "error")) + "\n"
	os.WriteFile(launcher, []byte(body), 0700)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	w := subprocessWriter{Staging: stage, Executable: launcher, PackageUID: 0}
	go func() {
		_, err := w.Write(context.WithValue(ctx, writeJobKey{}, "job1"), s, "source", dest, "file", dest)
		done <- err
	}()
	target := filepath.Join(dest, "file")
	deadline := time.After(3 * time.Second)
	for {
		entries, _ := os.ReadDir(dest)
		writing := false
		for _, entry := range entries {
			info, _ := entry.Info()
			if info != nil && info.Size() > 0 {
				writing = true
			}
		}
		if writing {
			break
		}
		select {
		case err := <-done:
			b, _ := os.ReadFile(filepath.Join(base, "error"))
			t.Fatalf("child ended before write: %v %s", err, b)
		case <-deadline:
			t.Fatal("write did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("expected cancellation")
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Fatal("temporary file survived cancellation", entries)
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("WRITE_FAILED left a partial destination")
	}
}
