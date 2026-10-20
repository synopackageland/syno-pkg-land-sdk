//go:build !linux

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func verifySocket(string, int, int, os.FileMode) error { return errLinux }

var errLinux = errors.New("executor requires Linux")

func ownedByRoot(os.FileInfo) bool  { return false }
func configureProcess(*exec.Cmd)    {}
func peerUID(net.Conn) (int, error) { return -1, errLinux }

// Like Linux openBeneath, refuse a symlink in any component, not only the last.
func openRegular(path string) (*os.File, error) {
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if info, err := os.Lstat(dir); err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("path")
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errLinux
	}
	return os.Open(path)
}

type subprocessWriter struct {
	Staging, Executable    string
	PackageUID, PackageGID int
}

func (subprocessWriter) Write(context.Context, Subject, string, string, string, string) (MoveReceipt, error) {
	return MoveReceipt{}, errLinux
}
func lockFile(*os.File) error { return errLinux }
func (subprocessWriter) Read(context.Context, Subject, string, string, int64) (string, int64, error) {
	return "", 0, errLinux
}
func runReadChild() error { return errLinux }

func bindDirectory(path string) (directoryIdentity, error) {
	current := ""
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		current += "/" + part
		info, err := os.Lstat(current)
		if err != nil {
			return directoryIdentity{}, err
		}
		if !info.IsDir() {
			return directoryIdentity{}, errLinux
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return directoryIdentity{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return directoryIdentity{}, errLinux
	}
	return directoryIdentity{uint64(st.Dev), st.Ino}, nil
}

func runMoveChild() error          { return errLinux }
func runPartCleanupChild() error   { return errLinux }
func runSourceCleanupChild() error { return errLinux }

const selfFD = "/dev/fd/"

func killGroup(pid int) { syscall.Kill(-pid, syscall.SIGKILL) }

func processStart(int) (string, error) { return "", errLinux }

func (subprocessWriter) Create() (*os.File, string, error) { return nil, "", errLinux }
func (subprocessWriter) Remove(string)                     {}
func (subprocessWriter) Used() (int64, error)              { return 0, errLinux }
