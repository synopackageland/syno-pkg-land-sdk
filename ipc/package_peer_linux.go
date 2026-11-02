package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func packagePeerSocket(fd int, name string) (unix.Stat_t, error) {
	var st unix.Stat_t
	err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil && (st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Mode&07777 != 0666) {
		err = errors.New("ipc: package peer socket must be owned by the package account with mode 0666")
	}
	return st, err
}

func startPackagePeerUnix(ctx context.Context, path string, authorize PeerAuthorizer, h func(context.Context, net.Conn, PeerCredentials)) (*PeerServer, error) {
	if os.Geteuid() == 0 {
		return nil, errors.New("ipc: run as the package account")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, name := filepath.Dir(path), filepath.Base(path)
	parent, err := unix.Open(filepath.Dir(dir), unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	if err := unix.Mkdirat(parent, filepath.Base(dir), 0711); err != nil && err != unix.EEXIST {
		return nil, err
	}
	fd, err := unix.Openat(parent, filepath.Base(dir), unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			unix.Close(fd)
		}
	}()
	var dirStat unix.Stat_t
	if err := unix.Fstat(fd, &dirStat); err != nil || dirStat.Uid != uint32(os.Geteuid()) || dirStat.Mode&07777 != 0711 {
		return nil, errors.New("ipc: package peer directory must be owned by the package account with mode 0711")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.New("ipc: package peer directory is already in use")
	}
	pinned := fmt.Sprintf("/proc/self/fd/%d/%s", fd, name)
	if stale, err := packagePeerSocket(fd, name); err == nil {
		c, dialErr := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", pinned)
		if dialErr == nil {
			c.Close()
			return nil, errors.New("ipc: package peer socket is live")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !errors.Is(dialErr, unix.ECONNREFUSED) {
			return nil, fmt.Errorf("ipc: cannot prove package peer socket stale: %w", dialErr)
		}
		current, err := packagePeerSocket(fd, name)
		if err != nil || current.Dev != stale.Dev || current.Ino != stale.Ino {
			return nil, errors.New("ipc: package peer socket changed during stale check")
		}
		if err := unix.Unlinkat(fd, name, 0); err != nil {
			return nil, err
		}
	} else if err != unix.ENOENT {
		return nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: pinned, Net: "unix"})
	if err != nil {
		return nil, err
	}
	l.SetUnlinkOnClose(false)
	// Remember the exact created inode even before applying its final mode.
	var created unix.Stat_t
	if err := unix.Fstatat(fd, name, &created, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		l.Close()
		return nil, err
	}
	cleanup := func() error {
		var current unix.Stat_t
		err := unix.Fstatat(fd, name, &current, unix.AT_SYMLINK_NOFOLLOW)
		if err == unix.ENOENT {
			return nil
		}
		if err != nil {
			return err
		}
		if current.Dev == created.Dev && current.Ino == created.Ino {
			return unix.Unlinkat(fd, name, 0)
		}
		return nil // Never unlink a replacement.
	}
	defer func() {
		if !owned {
			l.Close()
			_ = cleanup()
		}
	}()
	if err := unix.Fchmodat(fd, name, 0666, 0); err != nil {
		return nil, err
	}
	if _, err := packagePeerSocket(fd, name); err != nil {
		return nil, err
	}
	handlerCtx, cancel := context.WithCancel(ctx)
	s := &PeerServer{listener: l, ctx: handlerCtx, cancel: cancel, done: make(chan struct{}), closed: make(chan struct{}), conns: make(map[*peerConn]struct{}), slots: make(chan struct{}, 8)}
	s.cleanup = func() { defer unix.Close(fd); s.err = errors.Join(s.err, cleanup()) }
	owned = true
	// Reuse the bounded peer lifecycle, keeping the root executor's UID policy
	// separate from this caller-supplied, non-executor authorization decision.
	go s.servePackagePeers(ctx, l, authorize, h)
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.closed:
		}
	}()
	return s, nil
}

func (s *PeerServer) servePackagePeers(ctx context.Context, l *net.UnixListener, authorize PeerAuthorizer, h func(context.Context, net.Conn, PeerCredentials)) {
	// The shared loop authenticates first and bounds callbacks before invoking
	// this wrapper. A nil authorizer is deliberately fail closed.
	s.serveVerifiedPeers(ctx, l, nil, func(ctx context.Context, c net.Conn, p PeerCredentials) {
		if authorize == nil || !authorize(ctx, p) || ctx.Err() != nil {
			c.Close()
			return
		}
		h(ctx, c, p)
	})
}
