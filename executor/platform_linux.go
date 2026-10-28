//go:build linux

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func ownedByRoot(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == 0
}

// O_PATH allows fstat on the bound filesystem socket inode, rather than the
// anonymous socket descriptor (which has different metadata).
func verifySocket(path string, uid, gid int, mode os.FileMode) error {
	f, err := openBeneath("/", strings.TrimPrefix(path, "/"), 0x200000, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != mode || int(st.Uid) != uid || int(st.Gid) != gid {
		return errors.New("socket permissions")
	}
	return nil
}

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
}
func peerUID(c net.Conn) (int, error) {
	u, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errors.New("unix peer")
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return -1, err
	}
	uid := -1
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		cred, e := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		sockErr = e
		if e == nil {
			uid = int(cred.Uid)
		}
	})
	if err != nil {
		return -1, err
	}
	return uid, sockErr
}

// openBeneath rejects symlinks in every untrusted component, including parent
// directories. Directory descriptors pin the lookup despite concurrent renames.
func openBeneath(root, relative string, flags int, mode uint32) (*os.File, error) {
	if !validRelative(relative) {
		return nil, errors.New("relative path")
	}
	var dir *os.File
	var err error
	if root == "/" {
		dir, err = os.Open(root)
	} else if cleanPath(root) {
		dir, err = openBeneath("/", strings.TrimPrefix(root, "/"), syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	} else {
		return nil, errors.New("root path")
	}
	if err != nil {
		return nil, err
	}
	defer func() { dir.Close() }()
	parts := strings.Split(relative, "/")
	for i, part := range parts {
		f := flags
		if i < len(parts)-1 {
			f = 0x200000 | syscall.O_DIRECTORY // O_PATH: ancestor requires search, not listing rights
		}
		fd, err := syscall.Openat(int(dir.Fd()), part, f|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, mode)
		if err != nil {
			return nil, err
		}
		next := os.NewFile(uintptr(fd), part)
		if i == len(parts)-1 {
			return next, nil
		}
		dir.Close()
		dir = next
	}
	return nil, errors.New("path")
}
func openRegular(path string) (*os.File, error) {
	if !cleanPath(path) {
		return nil, errors.New("path")
	}
	f, err := openBeneath("/", strings.TrimPrefix(path, "/"), syscall.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("regular file required")
	}
	return f, nil
}

type subprocessWriter struct {
	Staging, Executable    string
	PackageUID, PackageGID int
}

func (w subprocessWriter) Write(ctx context.Context, s Subject, staging, dest, name, root string) (MoveReceipt, error) {
	job, _ := ctx.Value(writeJobKey{}).(string)
	f, _, err := w.openSource(staging)
	if err != nil {
		return MoveReceipt{}, err
	}
	info, err := f.Stat()
	f.Close()
	if err != nil {
		return MoveReceipt{}, err
	}
	// Checked on the opened file, before the destination or any child exists.
	if err := checkWriteSize(info.Size()); err != nil {
		return MoveReceipt{}, err
	}
	di, err := bindDirectory(dest)
	if err != nil {
		return MoveReceipt{}, err
	}
	guard, _ := ctx.Value(legacyPublishKey{}).(func(func() (MoveReceipt, error)) (MoveReceipt, error))
	if guard == nil {
		guard = func(publish func() (MoveReceipt, error)) (MoveReceipt, error) { return publish() }
	}
	conflict, _ := ctx.Value(writeConflictKey{}).(string)
	return w.Move(ctx, s, Operation{Job: job, Name: name, Conflict: conflict, Staging: staging, Size: info.Size()}, Delegation{Root: dest, Directory: di}, guard)
}

type realIdentity struct{}

func (realIdentity) Setgroups(gs []int) error    { return syscall.Setgroups(gs) }
func (realIdentity) Setresgid(a, b, c int) error { return syscall.Setresgid(a, b, c) }
func (realIdentity) Setresuid(a, b, c int) error { return syscall.Setresuid(a, b, c) }
func (realIdentity) IDs() (int, int, int, int) {
	return os.Getuid(), os.Geteuid(), os.Getgid(), os.Getegid()
}
func (realIdentity) Groups() ([]int, error) { return os.Getgroups() }
func validTemporary(name string) bool {
	return validName(name) && strings.HasPrefix(name, ".synopkgland-") && strings.HasSuffix(name, ".part")
}
func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }

func bindDirectory(path string) (directoryIdentity, error) {
	f, err := openBeneath("/", strings.TrimPrefix(path, "/"), syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return directoryIdentity{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return directoryIdentity{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return directoryIdentity{}, errors.New("directory")
	}
	return directoryIdentity{uint64(st.Dev), st.Ino}, nil
}
