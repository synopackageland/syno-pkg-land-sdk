package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFinishControlPurgesOfflineRuntimeGrantsAndStaleSocket(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	socket := filepath.Join(root, "socket")
	for _, path := range []string{filepath.Join(state, "runtime"), filepath.Join(state, "delegations"), socket} {
		os.MkdirAll(path, 0700)
		os.WriteFile(filepath.Join(path, "stale"), []byte("data"), 0600)
	}
	if err := finishControl(state, filepath.Join(state, "runtime"), socket, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{state, socket} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("remnant %s", path)
		}
	}
	if err := finishControl(state, filepath.Join(state, "runtime"), socket, true); err != nil {
		t.Fatal(err)
	}
}
func TestFinishStopKeepsGrantsButRemovesSocketAndPurgeRejectsForeignRuntime(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	socket := filepath.Join(root, "socket")
	os.MkdirAll(state, 0700)
	os.MkdirAll(socket, 0700)
	if err := finishControl(state, filepath.Join(state, "runtime"), socket, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal(err)
	}
	if err := finishControl(state, root, socket, true); err == nil {
		t.Fatal("accepted foreign runtime")
	}
}

func TestCreatedDirectoriesHonorModesUnderPrivateUmask(t *testing.T) {
	root := t.TempDir()
	old := syscall.Umask(0077)
	defer syscall.Umask(old)
	for _, mode := range []os.FileMode{0755, 0750, 0700} {
		path := filepath.Join(root, fmt.Sprintf("dir-%o", mode))
		info, err := createDirectory(path, mode, os.Geteuid(), os.Getegid())
		if err != nil {
			t.Fatal(err)
		}
		if !directoryMatches(info, mode, os.Geteuid(), os.Getegid()) {
			t.Fatal("umask corrupted directory contract", info.Mode())
		}
		if _, err := createDirectory(path, mode, os.Geteuid(), os.Getegid()); err == nil {
			t.Fatal("existing directory was adopted")
		}
	}
}
func TestDirectoryValidationRejectsExistingWrongModeOwnerAndSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "existing")
	os.Mkdir(path, 0700)
	info, _ := os.Lstat(path)
	if directoryMatches(info, 0755, os.Geteuid(), os.Getegid()) {
		t.Fatal("wrong mode accepted")
	}
	if directoryMatches(info, 0700, os.Geteuid()+1, os.Getegid()) {
		t.Fatal("wrong owner accepted")
	}
	if directoryMatches(info, 0700, os.Geteuid(), os.Getegid()+1) {
		t.Fatal("wrong group accepted")
	}
	link := path + "-link"
	os.Symlink(path, link)
	info, _ = os.Lstat(link)
	if directoryMatches(info, 0700, os.Geteuid(), os.Getegid()) {
		t.Fatal("symlink accepted")
	}
}

// The legacy write child linked its own copy without waiting for serve, so an
// orphan could still publish. Nothing spawns it since files.write moved to the
// move child; neither it nor its cleanup child may run at all.
func TestLegacyWriteChildrenAreNotCommands(t *testing.T) {
	for _, child := range []string{"write-child", "cleanup-child"} {
		if err := run([]string{child}); err == nil || err.Error() != "invalid command" {
			t.Fatalf("%s: %v", child, err)
		}
	}
}
