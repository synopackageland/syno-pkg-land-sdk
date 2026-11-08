//go:build linux

package memoryexec

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestBridgeNeverStagesToPersistentStorage(t *testing.T) {
	dir := t.TempDir()
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Open(dir, []byte("bridge"))
	if errors.Is(err, ErrUnavailable) {
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatal("failed memory staging wrote to disk")
		}
		t.Skip("no executable tmpfs; refusal verified")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil || st.Type != unix.TMPFS_MAGIC || st.Flags&unix.ST_NOEXEC != 0 {
		t.Fatal("bridge is not in executable memory", err)
	}
	b, err := io.ReadAll(f)
	if err != nil || string(b) != "bridge" {
		t.Fatal(string(b), err)
	}
	if _, err := f.WriteAt([]byte("attack"), 0); err == nil {
		t.Fatal("bridge descriptor remains writable")
	}
	after, _ := os.Stat(dir)
	if !executableMemory(dir) && !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("bridge changed the persistent directory")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("bridge left directory entries")
	}
	if _, err := os.Stat(filepath.Join(dir, "bridge")); !os.IsNotExist(err) {
		t.Fatal("bridge file was not unlinked")
	}
}

func TestBridgePayloadHasHardCap(t *testing.T) {
	for _, payload := range [][]byte{nil, make([]byte, (1<<20)+1)} {
		if _, err := Open(t.TempDir(), payload); !errors.Is(err, ErrUnavailable) {
			t.Fatal("unbounded or empty bridge accepted", err)
		}
	}
}
