package ipc

import (
	"context"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func peerCredentials(c *net.UnixConn) (PeerCredentials, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return PeerCredentials{}, err
	}
	var cred *unix.Ucred
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return PeerCredentials{}, err
	}
	if sockErr != nil {
		return PeerCredentials{}, sockErr
	}
	return PeerCredentials{UID: cred.Uid, GID: cred.Gid, PID: cred.Pid}, nil
}

// Pin root-owned components beneath the platform's /var/run anchor. The
// platform may symlink that anchor, but neither package-controlled child may
// be a symlink or writable by an unprivileged account.
func peerRun(create bool) (int, error) {
	fd, err := unix.Open("/var/run", unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	parts := []string{"synopkgland", packageID}
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			if create {
				if err := unix.Mkdirat(fd, parts[i], 0755); err != nil && err != unix.EEXIST {
					unix.Close(fd)
					return -1, err
				}
			}
			next, err := unix.Openat(fd, parts[i], unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			unix.Close(fd)
			if err != nil {
				return -1, err
			}
			fd = next
		}
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil || st.Uid != 0 || st.Mode&0022 != 0 {
			unix.Close(fd)
			return -1, errors.New("ipc: executor directory must be root-owned and not group/world writable")
		}
	}
	return fd, nil
}

func peerSocket(fd int, name string) (unix.Stat_t, error) {
	var st unix.Stat_t
	err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil && (st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Mode&07777 != 0660) {
		err = errors.New("ipc: executor socket must be root-owned with mode 0660")
	}
	return st, err
}

func startPeerUnix(ctx context.Context, run, name string, policy PeerPolicy, h func(context.Context, net.Conn, PeerCredentials)) (*PeerServer, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("ipc: peer server requires root executor identity")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := peerRun(true)
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			unix.Close(fd)
		}
	}()
	var st unix.Stat_t
	// Keep the executor's stricter root/0660 contract before applying the
	// same liveness and inode checks as the package HTTP socket.
	if stale, err := peerSocket(fd, name); err != unix.ENOENT {
		if err != nil || removeStaleSocket(fd, name, &stale) != nil {
			return nil, errors.New("ipc: executor socket already exists or cannot be inspected")
		}
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: fmt.Sprintf("/proc/self/fd/%d/%s", fd, name), Net: "unix"})
	if err != nil {
		return nil, err
	}
	l.SetUnlinkOnClose(false)
	defer func() {
		if !owned {
			l.Close()
		}
	}()
	if err := unix.Fchownat(fd, name, 0, int(policy.GID), unix.AT_SYMLINK_NOFOLLOW); err != nil {
		unix.Unlinkat(fd, name, 0)
		return nil, err
	}
	if err := unix.Fchmodat(fd, name, 0660, 0); err != nil {
		unix.Unlinkat(fd, name, 0)
		return nil, err
	}
	st, err = peerSocket(fd, name)
	if err != nil {
		return nil, err
	}
	handlerCtx, cancel := context.WithCancel(ctx)
	s := &PeerServer{listener: l, ctx: handlerCtx, cancel: cancel, done: make(chan struct{}), closed: make(chan struct{}), conns: make(map[*peerConn]struct{}), slots: make(chan struct{}, 8)}
	s.cleanup = func() {
		defer unix.Close(fd)
		current, err := peerSocket(fd, name)
		if err == nil && st.Dev == current.Dev && st.Ino == current.Ino {
			if err := unix.Unlinkat(fd, name, 0); err != nil {
				s.err = errors.Join(s.err, err)
			}
		} else if err != nil && err != unix.ENOENT {
			s.err = errors.Join(s.err, err)
		}
	}
	owned = true
	go s.servePeers(ctx, l, policy, h)
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.closed:
		}
	}()
	return s, nil
}

func (s *PeerServer) servePeers(ctx context.Context, l *net.UnixListener, policy PeerPolicy, h func(context.Context, net.Conn, PeerCredentials)) {
	s.serveVerifiedPeers(ctx, l, func(p PeerCredentials) bool { return p.UID == policy.UID }, h)
}

func (s *PeerServer) serveVerifiedPeers(ctx context.Context, l *net.UnixListener, filter func(PeerCredentials) bool, h func(context.Context, net.Conn, PeerCredentials)) {
	defer close(s.done)
	defer s.cleanup()
	var backoff time.Duration
	for {
		c, err := l.AcceptUnix()
		if err != nil && ctx.Err() == nil && transientAcceptError(err) {
			// Descriptor or buffer exhaustion is a capacity condition, not a
			// listener failure: keep retained peers and retry admission with a
			// bounded backoff. Close and cancellation still end the loop.
			backoff = nextAcceptBackoff(backoff)
			t := time.NewTimer(backoff)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
			case <-s.closed:
				t.Stop()
			}
			continue
		}
		backoff = 0
		if err != nil {
			if ctx.Err() != nil {
				s.err = ctx.Err()
			} else if !errors.Is(err, net.ErrClosed) {
				s.err = err
			}
			if s.err != nil {
				s.closeConnections()
			}
			return
		}
		peer, err := peerCredentials(c)
		if err != nil || filter != nil && !filter(peer) {
			c.Close()
			continue
		}
		select {
		case s.slots <- struct{}{}:
		default:
			c.Close()
			continue
		}
		t := &peerConn{Conn: c, owner: s}
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			t.handlerReturned()
			t.Close()
			continue
		}
		s.conns[t] = struct{}{}
		s.mu.Unlock()
		c.SetDeadline(time.Now().Add(runtimebudget.Operation))
		go func() {
			defer t.handlerReturned()
			h(s.ctx, t, peer)
		}()
	}
}

// transientAcceptError reports kernel resource exhaustion or an aborted
// pending connection, after which accept(2) can succeed again.
func transientAcceptError(err error) bool {
	return errors.Is(err, unix.EMFILE) || errors.Is(err, unix.ENFILE) || errors.Is(err, unix.ENOBUFS) ||
		errors.Is(err, unix.ENOMEM) || errors.Is(err, unix.ECONNABORTED)
}

func nextAcceptBackoff(d time.Duration) time.Duration {
	if d == 0 {
		return 5 * time.Millisecond
	}
	if d *= 2; d > time.Second {
		d = time.Second
	}
	return d
}

func dialPeerUnix(ctx context.Context, run, name string) (net.Conn, error) {
	fd, err := peerRun(false)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if _, err := peerSocket(fd, name); err != nil {
		return nil, err
	}
	c, err := (&net.Dialer{}).DialContext(ctx, "unix", fmt.Sprintf("/proc/self/fd/%d/%s", fd, name))
	if err != nil {
		return nil, err
	}
	peer, err := peerCredentials(c.(*net.UnixConn))
	if err != nil || peer.UID != 0 {
		c.Close()
		return nil, errors.New("ipc: executor server peer must be root")
	}
	return c, nil
}
