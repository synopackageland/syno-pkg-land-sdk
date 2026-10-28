package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func privateRun(run string) (int, error) {
	if os.Geteuid() == 0 {
		return -1, errors.New("ipc: run as the package account")
	}
	// DSM's var anchor may be a platform-owned symlink. Pin it once, then
	// never follow a run-directory or socket symlink beneath that anchor.
	parent, err := unix.Open(filepath.Dir(run), unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	defer unix.Close(parent)
	if err := unix.Mkdirat(parent, "run", 0700); err != nil && err != unix.EEXIST {
		return -1, err
	}
	fd, err := unix.Openat(parent, "run", unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 {
		unix.Close(fd)
		return -1, errors.New("ipc: var/run must be owned by the package account with mode 0700")
	}
	return fd, nil
}

func privateSocket(fd int, name string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Mode&07777 != 0600 || st.Uid != uint32(os.Geteuid()) {
		return errors.New("ipc: socket must be owned by the package account with mode 0600")
	}
	return nil
}

func serveUnix(ctx context.Context, run, name string, h http.Handler) error {
	fd, err := privateRun(run)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != unix.ENOENT {
		return errors.New("ipc: socket path already exists or cannot be inspected")
	}
	path := fmt.Sprintf("/proc/self/fd/%d/%s", fd, name)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	if err := unix.Fchmodat(fd, name, 0600, 0); err != nil {
		unix.Unlinkat(fd, name, 0)
		return err
	}
	if err := privateSocket(fd, name); err != nil {
		return err
	}
	unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	defer func() {
		var current unix.Stat_t
		if unix.Fstatat(fd, name, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && current.Ino == st.Ino && current.Dev == st.Dev {
			unix.Unlinkat(fd, name, 0)
		}
	}()
	server := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			server.Close()
		case <-done:
		}
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return ctx.Err()
	}
	return err
}

func dialUnix(ctx context.Context, run, name string) (net.Conn, error) {
	fd, err := privateRun(run)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if err := privateSocket(fd, name); err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", fmt.Sprintf("/proc/self/fd/%d/%s", fd, name))
}
