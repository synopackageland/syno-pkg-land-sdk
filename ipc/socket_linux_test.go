package ipc

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRemoveStaleSocketKeepsChangedIdentity(t *testing.T) {
	run := t.TempDir()
	fd, err := unix.Open(run, unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	path := filepath.Join(run, "stale.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	var original unix.Stat_t
	if err := unix.Fstatat(fd, "stale.sock", &original, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"inode", "device", "owner"} {
		t.Run(field, func(t *testing.T) {
			changed := original
			switch field {
			case "inode":
				changed.Ino++
			case "device":
				changed.Dev++
			case "owner":
				changed.Uid++
			}
			if err := removeStaleSocket(fd, "stale.sock", &changed); !errors.Is(err, errSocketExists) {
				t.Fatalf("changed identity accepted: %v", err)
			}
			var current unix.Stat_t
			if err := unix.Fstatat(fd, "stale.sock", &current, unix.AT_SYMLINK_NOFOLLOW); err != nil || current.Ino != original.Ino || current.Dev != original.Dev {
				t.Fatalf("socket removed or replaced: %v", err)
			}
		})
	}
	// A path that vanished after inspection needs no unlink.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleSocket(fd, "stale.sock", &original); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveStaleSocketKeepsUnprovenSocket(t *testing.T) {
	run := t.TempDir()
	fd, err := unix.Open(run, unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	// A stream probe gets EPROTOTYPE for a datagram socket, not proof that
	// nobody owns it. Keep it even though it has the expected owner/type.
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: filepath.Join(run, "datagram.sock"), Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var st unix.Stat_t
	if err := unix.Fstatat(fd, "datagram.sock", &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleSocket(fd, "datagram.sock", &st); !errors.Is(err, errSocketExists) {
		t.Fatalf("unproven socket accepted: %v", err)
	}
	var current unix.Stat_t
	if err := unix.Fstatat(fd, "datagram.sock", &current, unix.AT_SYMLINK_NOFOLLOW); err != nil || current.Ino != st.Ino || current.Dev != st.Dev {
		t.Fatalf("unproven socket removed or replaced: %v", err)
	}
}
