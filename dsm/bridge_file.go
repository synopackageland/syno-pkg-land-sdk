package dsm

import (
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/memoryexec"
	"os"
	"path/filepath"
	"syscall"
)

// bridgeFallbackDir is where a kernel without memfd_create (DSM 7 platforms on
// Linux 3.10) stages the bridge. Empty refuses, as before the fallback existed.
var bridgeFallbackDir string

// SetBridgeFallbackDir names the package's own temp directory,
// /var/packages/<package>/tmp. main calls it once, before serving.
func SetBridgeFallbackDir(dir string) {
	bridgeFallbackDir = dir
	prepareBridgeFallback(dir)
}

var errBridgeDir = errors.New("bridge fallback directory must be a private absolute directory")

// unlinkedBridge returns a read-only descriptor from executable tmpfs. The
// configured private directory is checked, but never used for disk fallback.
// Unlike a sealed memfd, a process
// running as the same package account could rewrite the file before it is
// unlinked; that account already controls the CGI, so the trust is unchanged.
func unlinkedBridge(dir string, payload []byte) (*os.File, error) {
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	return memoryexec.Open(dir, payload)
}

// privateDir accepts an absolute directory owned by this account and writable
// by no one else. It follows links: DSM's /var/packages/<pkg>/tmp is one.
func privateDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return errBridgeDir
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 || !ok || int(st.Uid) != os.Geteuid() {
		return errBridgeDir
	}
	return nil
}
