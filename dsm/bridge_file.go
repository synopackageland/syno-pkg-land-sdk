package dsm

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// bridgeFallbackDir is where a kernel without memfd_create (DSM 7 platforms on
// Linux 3.10) stages the bridge. Empty refuses, as before the fallback existed.
var bridgeFallbackDir string

// SetBridgeFallbackDir names the package's own temp directory,
// /var/packages/<package>/tmp. main calls it once, before serving.
func SetBridgeFallbackDir(dir string) { bridgeFallbackDir = dir }

var errBridgeDir = errors.New("bridge fallback directory must be a private absolute directory")

// unlinkedBridge stages payload in a fresh 0700 directory under dir and returns
// a read-only descriptor whose name is already gone. /tmp is noexec on DSM, so
// dir is the package's own exec-capable temp. Unlike a sealed memfd, a process
// running as the same package account could rewrite the file before it is
// unlinked; that account already controls the CGI, so the trust is unchanged.
func unlinkedBridge(dir string, payload []byte) (*os.File, error) {
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(dir, "bridge-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	path := filepath.Join(stage, "bridge")
	writer, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_, err = writer.Write(payload)
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(path, 0o500)
	}
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err = os.Remove(path); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
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
