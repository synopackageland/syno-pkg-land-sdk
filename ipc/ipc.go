// Package ipc serves private HTTP IPC for the non-root package account and
// reserves loopback ports for supervised children. It never returns a listener.
package ipc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

// The packaging CLI binds this for each Go binary.
var packageID string

var packageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func socketPath(path string) (string, string, error) {
	if !packageName.MatchString(packageID) {
		return "", "", errors.New("ipc: package identity is not bound by the build")
	}
	run := "/var/packages/" + packageID + "/var/run"
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) != run ||
		!strings.HasSuffix(filepath.Base(path), ".sock") || len(filepath.Base(path)) > 64 {
		return "", "", errors.New("ipc: socket must be a direct .sock child of this package's var/run")
	}
	return run, filepath.Base(path), nil
}

// ServeUnix serves h until ctx is canceled. The SDK creates a mode-0600 socket
// in the package's private mode-0700 var/run directory and refuses existing
// paths, including stale sockets. It never changes process-wide umask.
func ServeUnix(ctx context.Context, path string, h http.Handler) error {
	run, name, err := socketPath(path)
	if err != nil {
		return err
	}
	if h == nil {
		return errors.New("ipc: handler is required")
	}
	return serveUnix(ctx, run, name, h)
}

// DialUnix connects only to this package's private, owned mode-0600 socket.
// Use it as an http.Transport.DialContext implementation; ignore the HTTP
// transport's network/address arguments rather than dialing those arguments.
func DialUnix(ctx context.Context, path string) (net.Conn, error) {
	run, name, err := socketPath(path)
	if err != nil {
		return nil, err
	}
	return dialUnix(ctx, run, name)
}
