package ipc

import (
	"context"
	"errors"
	"net"
	"path/filepath"
)

// PeerAuthorizer decides whether a kernel-authenticated peer may enter a
// package protocol. It must honor cancellation and return promptly. Admission
// is not per-operation authorization: handlers must still validate each request.
type PeerAuthorizer func(context.Context, PeerCredentials) bool

// StartPackagePeerUnix starts a Linux package-account peer server, without an
// executor build or privilege elevation. Root is refused. The absolute path
// must name a .sock file in a dedicated package-owned directory, beneath a
// trusted parent (DSM's package var anchor may be a symlink). The SDK creates
// or verifies the directory with exact mode 0711, and the socket with mode
// 0666. It never changes umask or repairs existing ownership/permissions.
// Only an owned 0666 socket proven stale by ECONNREFUSED may be removed.
// A directory lock prevents competing SDK servers from replacing each other;
// use a separate directory for each listener.
//
// Every connection is authenticated with SO_PEERCRED before authorize runs.
// Nil authorize denies all peers; false closes the connection without calling
// h. Eight slots bound both authorization and handlers/retained connections.
// Authorize and h share a cancelable context and a five-second initial socket
// deadline. As with StartPeerUnix, h owns the connection and must close it,
// bound frames, enforce current operation authorization and manage deadlines.
// Close/cancellation closes retained peers; StopAccepting preserves them.
func StartPackagePeerUnix(ctx context.Context, path string, authorize PeerAuthorizer, h func(context.Context, net.Conn, PeerCredentials)) (*PeerServer, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Ext(path) != ".sock" ||
		!packageName.MatchString(filepath.Base(path[:len(path)-len(".sock")])) || len(filepath.Base(path)) > 64 {
		return nil, errors.New("ipc: package peer socket requires a clean absolute named .sock path")
	}
	if h == nil {
		return nil, errors.New("ipc: peer handler is required")
	}
	return startPackagePeerUnix(ctx, path, authorize, h)
}
