//go:build linux

package dsm

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestNativeBridgeIsSealedBeforeExecution(t *testing.T) {
	if fd, err := unix.MemfdCreate("probe", unix.MFD_CLOEXEC); errors.Is(err, unix.ENOSYS) {
		t.Skip("kernel has no memfd_create; covered by the fallback tests")
	} else if err == nil {
		unix.Close(fd)
	}
	file, err := sealedBridge([]byte("immutable"))
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
	seals, err := unix.FcntlInt(file.Fd(), unix.F_GET_SEALS, 0)
	if err != nil || seals != unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL {
		t.Fatal(seals, err)
	}
}

func TestEveryShippedArchHasBothBridges(t *testing.T) {
	for arch, machine := range map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64, "arm": elf.EM_ARM} {
		for name, bridges := range map[string]map[string][]byte{"admin": adminBridges, "apppriv": appPrivBridges} {
			file, err := elf.NewFile(bytes.NewReader(bridges[arch]))
			if err != nil || file.Machine != machine {
				t.Fatalf("%s bridge for %s: %v %v", name, arch, file, err)
			}
		}
	}
}

func withMemfd(t *testing.T, create func(string, int) (int, error), fallback string) {
	t.Helper()
	saved, savedDir := memfdCreate, bridgeFallbackDir
	t.Cleanup(func() { memfdCreate, bridgeFallbackDir = saved, savedDir })
	memfdCreate = create
	SetBridgeFallbackDir(fallback)
}

func TestBridgeFallsBackToPackageTempOnlyWithoutMemfd(t *testing.T) {
	dir := t.TempDir()
	withMemfd(t, func(string, int) (int, error) { return -1, unix.ENOSYS }, dir)
	file, err := sealedBridge([]byte("bridge"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil || string(got) != "bridge" {
		t.Fatalf("payload %q %v", got, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("fallback left %v", entries)
	}
}

func TestBridgeRefusesOtherMemfdFailuresAndUnsetFallback(t *testing.T) {
	withMemfd(t, func(string, int) (int, error) { return -1, unix.EPERM }, t.TempDir())
	if _, err := sealedBridge([]byte("bridge")); !errors.Is(err, unix.EPERM) {
		t.Fatalf("EPERM must not fall back: %v", err)
	}
	withMemfd(t, func(string, int) (int, error) { return -1, unix.ENOSYS }, "")
	if _, err := sealedBridge([]byte("bridge")); err == nil {
		t.Fatal("ENOSYS without a package temp directory must refuse")
	}
}

// Read-only device fixture. The runner provides existing principals and executes
// this test binary from a memfd. No files, users, groups or NAS settings change.
func TestNativeAdministratorDeviceFixture(t *testing.T) {
	fixture := os.Getenv("DSM_ADMIN_DEVICE_FIXTURE")
	if fixture == "" {
		t.Skip("device runner supplies existing principals")
	}
	for _, row := range strings.Split(fixture, ";") {
		fields := strings.SplitN(row, "=", 2)
		if len(fields) != 2 {
			t.Fatal("bad fixture")
		}
		want := fields[0] == "1"
		got, err := IsAdministrator(context.Background(), fields[1])
		if err != nil || got != want {
			t.Fatalf("native role %q: %v %v want=%v", fields[1], got, err, want)
		}
	}
}

// Execute the same bridge transport for local and directory identities. Group
// lists, the process account and an account named "admin" are not substitutes.
func TestNativeRoleBridgeForLocalAndDirectoryPrincipals(t *testing.T) {
	saved := adminBridges
	t.Cleanup(func() { adminBridges = saved })
	payload := []byte("#!/bin/sh\ncase \"$1\" in operator|'CORP\\operator'|operator@example.test) echo 1;; guest) echo 0;; *) exit 2;; esac\n")
	adminBridges = map[string][]byte{runtime.GOARCH: payload}
	for _, name := range []string{"operator", `CORP\operator`, "operator@example.test", "guest", "missing"} {
		got, err := IsAdministrator(context.Background(), name)
		if name == "missing" {
			if err == nil || got {
				t.Fatalf("bridge failure granted a role: %v %v", got, err)
			}
			continue
		}
		if err != nil || got != (name != "guest") {
			t.Fatalf("%q role=%v err=%v", name, got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := IsAdministrator(ctx, "operator"); err == nil || got {
		t.Fatalf("canceled check granted a role: %v %v", got, err)
	}
}
