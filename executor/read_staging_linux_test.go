//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReadStagingReservationsStayBounded(t *testing.T) {
	e := &Executor{ReadStaging: dirStaging{t.TempDir()}}
	release, err := e.reserveReadStaging(maxMemoryReadStaging)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.reserveReadStaging(1); !errors.Is(err, errRawSpace) {
		t.Fatal("parallel reads exceeded memory capacity", err)
	}
	release()
	release, err = e.reserveReadStaging(maxMemoryReadStaging)
	if err != nil {
		t.Fatal("reservation leaked", err)
	}
	release()
}

func TestServiceStopFlushesBufferedAudit(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("service socket ownership requires root in an isolated container")
	}
	e, _, _ := setup(t)
	stop, err := openService(context.Background(), e, os.Getuid(), os.Getgid(), filepath.Join(t.TempDir(), "executor.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.AuditReadOnly(auditEvent(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Store.Dir, "audit.jsonl")); !os.IsNotExist(err) {
		t.Fatal("buffered event was written before stop")
	}
	stop()
	if len(auditEvents(t, e.Store)) != 1 {
		t.Fatal("service stop lost buffered events")
	}
}

func TestReadStagingRequiresBoundedTmpfs(t *testing.T) {
	// A private directory on disk must not be accepted as read staging.
	var st unix.Statfs_t
	if err := unix.Statfs(t.TempDir(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Type != unix.TMPFS_MAGIC {
		w := subprocessWriter{Staging: filepath.Join(t.TempDir(), "read-staging"), PackageUID: os.Getuid(), PackageGID: os.Getgid(), Memory: true}
		if _, err := w.Available(); err == nil {
			t.Fatal("read staging fell back to disk")
		}
		if _, err := os.Stat(w.Staging); !os.IsNotExist(err) {
			t.Fatal("memory staging probe created a disk directory")
		}
	}
	if err := unix.Statfs("/dev/shm", &st); err != nil || st.Type != unix.TMPFS_MAGIC {
		t.Skip("no development tmpfs")
	}
	dir, err := os.MkdirTemp("/dev/shm", "read-staging-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	w := subprocessWriter{Staging: filepath.Join(dir, "read-staging"), PackageUID: os.Getuid(), PackageGID: os.Getgid(), Memory: true}
	free, err := w.Available()
	if err != nil || free > maxMemoryReadStaging {
		t.Fatal("unbounded memory staging", free, err)
	}
	for i := 0; i < maxMemoryReadFiles; i++ {
		f, _, err := w.Create()
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	if _, _, err := w.Create(); err == nil {
		t.Fatal("unbounded staging file count")
	}
	if entries, _ := os.ReadDir(w.Staging); len(entries) != maxMemoryReadFiles {
		t.Fatal("unexpected staging entry count")
	}
}

func TestRawReadAuditAndStagingUseMemory(t *testing.T) {
	e, f, id, _ := rawSetup(t, 128<<10, nil)
	memory := dirStaging{t.TempDir()}
	persistent := dirStaging{t.TempDir()}
	e.ReadStaging, e.RootStaging = memory, persistent
	f.stdout = []byte("Content-Type: application/octet-stream\r\n\r\n" + strings.Repeat("x", 64<<10))
	before, err := os.ReadFile(filepath.Join(e.Store.Dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	r := rawCall(e, id)
	if !r.OK {
		t.Fatal(r)
	}
	reply := r.Result.(map[string]any)
	if reply["memory_staging"] != true {
		t.Fatal("memory staging was not identified for the client")
	}
	if entries, _ := os.ReadDir(persistent.dir); len(entries) != 0 {
		t.Fatal("read spilled into persistent staging")
	}
	after, _ := os.ReadFile(filepath.Join(e.Store.Dir, "audit.jsonl"))
	if string(after) != string(before) {
		t.Fatal("raw read synchronously wrote audit")
	}
}
