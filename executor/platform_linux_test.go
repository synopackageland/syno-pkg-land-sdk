//go:build linux

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

type switchStub struct {
	calls []string
	fail  string
	s     Subject
	wrong bool
}

func (s *switchStub) step(name string) error {
	s.calls = append(s.calls, name)
	if s.fail == name {
		return errors.New("fail")
	}
	return nil
}
func (s *switchStub) Seal() error                    { return s.step("seal") }
func (s *switchStub) VerifyPrivileges(Subject) error { return s.step("verify") }
func (s *switchStub) Setgroups([]int) error          { return s.step("groups") }
func (s *switchStub) Setresgid(int, int, int) error  { return s.step("gid") }
func (s *switchStub) Setresuid(int, int, int) error  { return s.step("uid") }
func (s *switchStub) IDs() (int, int, int, int) {
	if s.wrong {
		return 0, 0, 0, 0
	}
	return s.s.UID, s.s.UID, s.s.GID, s.s.GID
}
func (s *switchStub) Groups() ([]int, error) { return s.s.Groups, nil }
func TestDropOrderAndFailures(t *testing.T) {
	subject := Subject{UID: 1200, GID: 100, Groups: []int{100, 123}}
	for _, fail := range []string{"", "seal", "groups", "gid", "uid", "verify"} {
		s := &switchStub{fail: fail, s: subject}
		err := dropSealedIdentity(s, subject)
		if (err == nil) != (fail == "") {
			t.Fatal(err)
		}
		expected := []string{"seal", "groups", "gid", "uid", "verify"}
		if fail != "" {
			for i, n := range expected {
				if n == fail {
					expected = expected[:i+1]
					break
				}
			}
		}
		if !reflect.DeepEqual(s.calls, expected) {
			t.Fatal(s.calls)
		}
	}
	s := &switchStub{s: subject, wrong: true}
	if dropSealedIdentity(s, subject) == nil {
		t.Fatal("identity not verified")
	}
}
func TestLinuxStagingSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "source"), []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	os.Symlink(filepath.Join(outside, "source"), filepath.Join(root, "file"))
	for _, p := range []string{"escape/source", "file", "../source"} {
		if f, err := openBeneath(root, p, syscall.O_RDONLY, 0); err == nil {
			f.Close()
			t.Fatal("symlink", p)
		}
	}
}
func TestLinuxPeerCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, err := ln.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		uid, err := peerUID(c)
		if err == nil && uid != os.Getuid() {
			err = errors.New("uid mismatch")
		}
		done <- err
	}()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestLinuxPIDLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pid")
	a, _ := os.Create(path)
	defer a.Close()
	b, _ := os.OpenFile(path, os.O_RDWR, 0600)
	defer b.Close()
	if lockFile(a) != nil || lockFile(b) == nil {
		t.Fatal("duplicate lock")
	}
}
func TestLinuxStagingRootSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(outside, "source"), []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(root, "stage"))
	if f, err := openBeneath(filepath.Join(root, "stage"), "source", syscall.O_RDONLY, 0); err == nil {
		f.Close()
		t.Fatal("staging root symlink accepted")
	}
}
func TestLinuxStagingHardlinkAndOwner(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "source")
	os.WriteFile(p, []byte("secret"), 0600)
	subject := Subject{UID: 1200, GID: 100, Groups: []int{100}}
	w := subprocessWriter{Staging: root, Executable: "/must-not-execute", PackageUID: os.Getuid() + 1}
	if _, err := w.Write(context.WithValue(context.Background(), writeJobKey{}, "job1"), subject, "source", root, "new", root); err == nil || !strings.Contains(err.Error(), "owner or hardlink") {
		t.Fatal(err)
	}
	w.PackageUID = os.Getuid()
	os.Link(p, filepath.Join(root, "linked"))
	if _, err := w.Write(context.WithValue(context.Background(), writeJobKey{}, "job1"), subject, "source", root, "new", root); err == nil || !strings.Contains(err.Error(), "owner or hardlink") {
		t.Fatal(err)
	}
}
