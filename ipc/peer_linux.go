package ipc

import (
	"context"
	"errors"
	"fmt"
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

func servePeerUnix(ctx context.Context, run, name string, policy PeerPolicy, h func(context.Context, net.Conn, PeerCredentials)) error {
	if os.Geteuid() != 0 {
		return errors.New("ipc: peer server requires root executor identity")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := peerRun(true)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != unix.ENOENT {
		return errors.New("ipc: executor socket already exists or cannot be inspected")
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: fmt.Sprintf("/proc/self/fd/%d/%s", fd, name), Net: "unix"})
	if err != nil {
		return err
	}
	l.SetUnlinkOnClose(false)
	defer l.Close()
	if err := unix.Fchownat(fd, name, 0, int(policy.GID), unix.AT_SYMLINK_NOFOLLOW); err != nil {
		unix.Unlinkat(fd, name, 0)
		return err
	}
	if err := unix.Fchmodat(fd, name, 0660, 0); err != nil {
		unix.Unlinkat(fd, name, 0)
		return err
	}
	st, err = peerSocket(fd, name)
	if err != nil {
		return err
	}
	defer func() {
		current, err := peerSocket(fd, name)
		if err == nil && st.Dev == current.Dev && st.Ino == current.Ino {
			unix.Unlinkat(fd, name, 0)
		}
	}()
	return servePeers(ctx, l, policy, h)
}

func servePeers(ctx context.Context, l *net.UnixListener, policy PeerPolicy, h func(context.Context, net.Conn, PeerCredentials)) error {
	s := &Server{conns: make(map[*trackedConn]struct{})}
	done := make(chan struct{})
	defer close(done)
	defer s.closeConnections()
	go func() {
		select {
		case <-ctx.Done():
			s.closeConnections()
			l.Close()
		case <-done:
		}
	}()
	slots := make(chan struct{}, 8)
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		peer, err := peerCredentials(c)
		if err != nil || peer.UID != policy.UID {
			c.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			c.Close()
			continue
		}
		t := &trackedConn{Conn: c, owner: s}
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			c.Close()
			<-slots
			continue
		}
		s.conns[t] = struct{}{}
		s.mu.Unlock()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		go func() {
			defer func() { t.Close(); <-slots }()
			h(ctx, t, peer)
		}()
	}
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
