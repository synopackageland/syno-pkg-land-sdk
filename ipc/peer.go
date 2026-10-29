package ipc

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
)

// Only a separately approved executor payload may bind this to "executor".
// Ordinary CGI/worker builds bind "none"; this library never elevates identity.
var executorTier string

type PeerCredentials struct {
	UID uint32
	GID uint32
	PID int32
}

// PeerPolicy admits exactly one numeric UID and gives its group socket access.
// UID zero is an explicit root-to-root control channel.
type PeerPolicy struct {
	UID uint32
	GID uint32
}

func peerPath(path string) (string, string, error) {
	if !packageName.MatchString(packageID) {
		return "", "", errors.New("ipc: package identity is not bound by the build")
	}
	run := "/var/run/synopkgland/" + packageID
	name := filepath.Base(path)
	if filepath.Clean(path) != path || filepath.Dir(path) != run || len(name) > 64 ||
		!packageName.MatchString(strings.TrimSuffix(name, ".sock")) || filepath.Ext(name) != ".sock" {
		return "", "", errors.New("ipc: peer socket must be a named .sock child of the package executor run directory")
	}
	return run, name, nil
}

// StartPeerUnix synchronously binds and secures a root-owned 0660 socket.
// It requires Linux, euid zero AND the executor-tier build binding. SO_PEERCRED
// admits the selected UID before invoking h. The handler owns its connection:
// returning does not close it; call Close when finished, or retain it until
// server Close, context cancellation or process exit. At most eight open peers
// are admitted, including retained peers and still-running callbacks, with a
// five-second initial deadline.
// The handler must bound frame sizes, validate protocol/authorization, honor
// cancellation and manage deadlines for longer exchanges. No listener or
// privilege elevation is exposed. Existing sockets are always refused.
func StartPeerUnix(ctx context.Context, path string, policy PeerPolicy, h func(context.Context, net.Conn, PeerCredentials)) (*PeerServer, error) {
	if executorTier != "executor" {
		return nil, errors.New("ipc: peer server requires a separately approved executor-tier build")
	}
	run, name, err := peerPath(path)
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, errors.New("ipc: peer handler is required")
	}
	return startPeerUnix(ctx, run, name, policy, h)
}

// ServePeerUnix is the blocking form of StartPeerUnix. It closes each peer
// when h returns and closes all peers on cancellation or a serving error.
func ServePeerUnix(ctx context.Context, path string, policy PeerPolicy, h func(context.Context, net.Conn, PeerCredentials)) error {
	if h == nil {
		return errors.New("ipc: peer handler is required")
	}
	server, err := StartPeerUnix(ctx, path, policy, func(ctx context.Context, c net.Conn, p PeerCredentials) {
		defer c.Close()
		h(ctx, c, p)
	})
	if err != nil {
		return err
	}
	defer server.Close()
	return server.Wait()
}

// DialPeerUnix connects to this package's protected executor socket, validates
// root ownership and mode 0660, and authenticates the server UID as zero using
// SO_PEERCRED. It does not approve or start an executor.
func DialPeerUnix(ctx context.Context, path string) (net.Conn, error) {
	run, name, err := peerPath(path)
	if err != nil {
		return nil, err
	}
	return dialPeerUnix(ctx, run, name)
}
