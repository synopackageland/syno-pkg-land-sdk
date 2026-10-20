//go:build unix

package idgateway

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockFile holds an exclusive flock on path until the returned func runs; it
// serializes the daemon and the account command on the same state files.
func lockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
