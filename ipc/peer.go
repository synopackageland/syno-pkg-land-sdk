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

// ServePeerUnix serves a bounded raw protocol on a root-owned 0660 socket.
// It requires Linux, euid zero AND the executor-tier build binding. The SDK
// checks SO_PEERCRED before invoking h, limits concurrency to eight peers and
// sets a five-second connection deadline. The handler must bound frame sizes,
// validate its protocol/authorization and honor cancellation. No HTTP or
// privilege elevation is installed. Existing sockets are always refused.
func ServePeerUnix(ctx context.Context, path string, policy PeerPolicy, h func(context.Context, net.Conn, PeerCredentials)) error {
	if executorTier != "executor" {
		return errors.New("ipc: peer server requires a separately approved executor-tier build")
	}
	run, name, err := peerPath(path)
	if err != nil {
		return err
	}
	if h == nil {
		return errors.New("ipc: peer handler is required")
	}
	return servePeerUnix(ctx, run, name, policy, h)
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
