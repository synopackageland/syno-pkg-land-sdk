//go:build linux

package main

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestBoundSocketPermissionsAreVerifiedOnFilesystemInode(t *testing.T) {
	root := t.TempDir()
	old := syscall.Umask(0077)
	defer syscall.Umask(old)
	socket := filepath.Join(root, "executor.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err = os.Chown(socket, os.Geteuid(), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(socket, 0660); err != nil {
		t.Fatal(err)
	}
	if err = verifySocket(socket, os.Geteuid(), os.Getegid(), 0660); err != nil {
		t.Fatal(err)
	}
	if verifySocket(socket, os.Geteuid()+1, os.Getegid(), 0660) == nil {
		t.Fatal("wrong owner accepted")
	}
	if verifySocket(socket, os.Geteuid(), os.Getegid()+1, 0660) == nil {
		t.Fatal("wrong group accepted")
	}
	if verifySocket(socket, os.Geteuid(), os.Getegid(), 0600) == nil {
		t.Fatal("wrong mode accepted")
	}
	link := socket + "-link"
	os.Symlink(socket, link)
	if verifySocket(link, os.Geteuid(), os.Getegid(), 0660) == nil {
		t.Fatal("symlink accepted")
	}
}
