// Package ipc serves private HTTP IPC for the non-root package account and
// reserves loopback ports for supervised children. ListenTCP additionally returns
// a raw TCP listener constrained by the manifest policy compiled by the CLI.
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
	return socketPathFor(packageID, path)
}

func socketPathFor(pkg, path string) (string, string, error) {
	if !packageName.MatchString(pkg) {
		return "", "", errors.New("ipc: package identity is not bound by the build")
	}
	run := "/var/packages/" + pkg + "/var/run"
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) != run ||
		!strings.HasSuffix(filepath.Base(path), ".sock") || len(filepath.Base(path)) > 64 {
		return "", "", errors.New("ipc: socket must be a direct .sock child of this package's var/run")
	}
	return run, filepath.Base(path), nil
}

// ServeUnix serves h until ctx is canceled. The SDK creates a mode-0600 socket
// in the package's private mode-0700 var/run directory. It replaces a stale
// socket owned by this account only after a refused/missing connection and an
// unchanged inode/device check; other existing paths are refused and kept.
// It never changes process-wide umask.
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

// DialPackageUnix is DialUnix for a binary that was not built with the package
// identity bound (the identity gateway): the caller names the package, and the
// same path rules and ownership/mode checks apply.
func DialPackageUnix(ctx context.Context, pkg, path string) (net.Conn, error) {
	run, name, err := socketPathFor(pkg, path)
	if err != nil {
		return nil, err
	}
	return dialUnix(ctx, run, name)
}

// ValidPackageSocket reports whether path is a direct .sock child of pkg's var/run.
func ValidPackageSocket(pkg, path string) error {
	_, _, err := socketPathFor(pkg, path)
	return err
}
