//go:build linux

// Package memoryexec stages legacy native bridges without persistent writes.
package memoryexec

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var ErrUnavailable = errors.New("executable memory filesystem unavailable")

// Open returns a read-only, already-unlinked executable. The payload and the
// temporary directory live only on verified executable tmpfs. No TMPDIR or
// disk fallback is used; noexec-only platforms fail closed.
func Open(preferred string, payload []byte) (*os.File, error) {
	if len(payload) == 0 || len(payload) > 1<<20 {
		return nil, ErrUnavailable
	}
	for _, base := range []string{preferred, "/run", "/dev/shm", "/tmp"} {
		if !filepath.IsAbs(base) || !executableMemory(base) {
			continue
		}
		dir, err := os.MkdirTemp(base, "synopkgland-bridge-")
		if err != nil {
			continue
		}
		f, err := stage(dir, payload)
		os.RemoveAll(dir)
		if err != nil {
			return nil, err
		}
		return f, nil
	}
	return nil, ErrUnavailable
}

func executableMemory(path string) bool {
	var st unix.Statfs_t
	return unix.Statfs(path, &st) == nil && st.Type == unix.TMPFS_MAGIC && st.Flags&unix.ST_NOEXEC == 0
}

func stage(dir string, payload []byte) (*os.File, error) {
	if !executableMemory(dir) {
		return nil, ErrUnavailable
	}
	path := filepath.Join(dir, "bridge")
	writer, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	_, err = writer.Write(payload)
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(path, 0500)
	}
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err = os.Remove(path); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
