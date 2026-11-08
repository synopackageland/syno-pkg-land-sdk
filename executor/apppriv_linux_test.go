//go:build linux

package main

import (
	"context"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/memoryexec"
	"io"
	"os"
	"syscall"
	"testing"
)

// A stand-in bridge: answers 1 only for the exact argument vector.
const echoBridge = "#!/bin/sh\n[ $# = 3 ] && [ \"$1\" = alice ] && [ \"$2\" = SYNO.SDS.App.Instance ] && [ \"$3\" = 192.0.2.1 ] && echo 1 || echo 0\n"

func TestAppPrivBridgeGetsExactlyPrincipalAppAndAddress(t *testing.T) {
	b := appPrivBridge{Dir: t.TempDir(), payload: []byte(echoBridge)}
	if ok, err := b.Check(context.Background(), "alice", "SYNO.SDS.App.Instance", "192.0.2.1"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := b.Check(context.Background(), "alice", "SYNO.SDS.App.Instance", "127.0.0.1"); err != nil || ok {
		t.Fatal(ok, err)
	}
}

func TestAppPrivBridgeFailureRefuses(t *testing.T) {
	b := appPrivBridge{Dir: t.TempDir(), payload: []byte("#!/bin/sh\necho 1\nexit 2\n")}
	if ok, err := b.Check(context.Background(), "alice", "SYNO.SDS.App.Instance", "192.0.2.1"); err == nil || ok {
		t.Fatal("a failed bridge allowed", ok, err)
	}
}

func withMemfd(t *testing.T, create func() (int, error)) {
	t.Helper()
	saved := memfdCreate
	t.Cleanup(func() { memfdCreate = saved })
	memfdCreate = create
}

func TestAppPrivBridgeIsSealedBeforeExecution(t *testing.T) {
	if fd, err := memfdCreate(); errors.Is(err, syscall.ENOSYS) {
		t.Skip("kernel has no memfd_create; covered by the fallback tests")
	} else if err == nil {
		syscall.Close(fd)
	}
	file, err := appPrivBridge{}.stage([]byte("immutable"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err = file.WriteAt([]byte("attack"), 0); err == nil {
		t.Fatal("sealed executable remains writable")
	}
	if err = file.Truncate(0); err == nil {
		t.Fatal("sealed executable can shrink")
	}
}

func TestAppPrivBridgeFallsBackToTheProtectedDirectoryOnlyWithoutMemfd(t *testing.T) {
	dir := t.TempDir()
	requireMemoryBridge(t, dir)
	withMemfd(t, func() (int, error) { return -1, syscall.ENOSYS })
	file, err := appPrivBridge{Dir: dir}.stage([]byte("bridge"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if got, err := io.ReadAll(file); err != nil || string(got) != "bridge" {
		t.Fatalf("payload %q %v", got, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("fallback left %v", entries)
	}
	withMemfd(t, func() (int, error) { return -1, syscall.EPERM })
	if _, err := (appPrivBridge{Dir: dir}).stage([]byte("bridge")); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("EPERM must not fall back: %v", err)
	}
	withMemfd(t, func() (int, error) { return -1, syscall.ENOSYS })
	if _, err := (appPrivBridge{}).stage([]byte("bridge")); err == nil {
		t.Fatal("ENOSYS without a protected directory must refuse")
	}
}

func TestPreparedLegacyBridgeDoesNotRestagePerRead(t *testing.T) {
	dir := t.TempDir()
	requireMemoryBridge(t, dir)
	withMemfd(t, func() (int, error) { return -1, syscall.ENOSYS })
	payload := []byte("bridge")
	b := (appPrivBridge{Dir: dir, payload: payload}).prepare(payload)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	// Once prepared at startup, calls work from the unlinked descriptor even
	// without the fallback directory. A second call must not share a seek.
	for i := 0; i < 2; i++ {
		f, err := b.stage(payload)
		if err != nil {
			t.Fatal("per-call staging attempted", err)
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil || string(data) != "bridge" {
			t.Fatal("prepared descriptor", string(data), err)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("per-call check recreated persistent directory")
	}
}

func requireMemoryBridge(t *testing.T, dir string) {
	t.Helper()
	f, err := memoryexec.Open(dir, []byte("probe"))
	if errors.Is(err, memoryexec.ErrUnavailable) {
		t.Skip("executable tmpfs unavailable; legacy bridge must refuse")
	}
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}
