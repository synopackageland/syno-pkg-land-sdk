package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

var errSocketExists = errors.New("ipc: socket path already exists or cannot be inspected")

// removeStaleSocket unlinks a socket left behind by a previous process that
// did not shut down cleanly (crash, SIGKILL, upgrade). Only a socket owned by
// this account that nobody listens on is removed: a live listener, another
// file type or a foreign owner is refused and kept.
func removeStaleSocket(fd int, name string, st *unix.Stat_t) error {
	if st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Uid != uint32(os.Geteuid()) {
		return errSocketExists
	}
	conn, err := net.DialTimeout("unix", fmt.Sprintf("/proc/self/fd/%d/%s", fd, name), time.Second)
	if err == nil {
		conn.Close()
		return errSocketExists
	}
	if !errors.Is(err, unix.ECONNREFUSED) && !errors.Is(err, unix.ENOENT) {
		return errSocketExists
	}
	var current unix.Stat_t
	if err := unix.Fstatat(fd, name, &current, unix.AT_SYMLINK_NOFOLLOW); err == unix.ENOENT {
		return nil
	} else if err != nil || current.Ino != st.Ino || current.Dev != st.Dev {
		return errSocketExists
	}
	if err := unix.Unlinkat(fd, name, 0); err != nil && err != unix.ENOENT {
		return err
	}
	return nil
}
