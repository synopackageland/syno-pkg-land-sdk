//go:build unix

package datamigrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// lock takes the exclusive flock on dataDir's LockFile and returns the
// function that releases it. A held lock queues the caller in the kernel for
// as long as the holder runs: a slow NAS or swap only makes the wait longer,
// so no clock fails it. Only ctx ends the wait; a holder that exits or
// crashes releases the lock with its file descriptor.
func lock(ctx context.Context, dataDir string) (func(), error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	// Read-only is enough for flock. 0644 lets the package account open a lock
	// file that a lifecycle script running as root created first; it holds no
	// data.
	f, err := os.OpenFile(filepath.Join(dataDir, LockFile), os.O_RDONLY|os.O_CREATE|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", LockFile, err)
	}
	fd := int(f.Fd())
	release := func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = f.Close() }
	err = flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return release, nil
	}
	if !errors.Is(err, unix.EWOULDBLOCK) {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", LockFile, err)
	}
	if ctx.Done() == nil {
		if err := flock(fd, unix.LOCK_EX); err != nil {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", LockFile, err)
		}
		return release, nil
	}
	// Wait in the kernel's queue on another goroutine so ctx can end the wait.
	// After a cancel that goroutine still owns the file: it closes it, and so
	// drops the lock, once its flock returns.
	acquired := make(chan error, 1)
	go func() { acquired <- flock(fd, unix.LOCK_EX) }()
	select {
	case err := <-acquired:
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", LockFile, err)
		}
		return release, nil
	case <-ctx.Done():
		go func() { <-acquired; f.Close() }()
		return nil, fmt.Errorf("datamigrate: waiting for another migration run: %w", ctx.Err())
	}
}

// flock retries a call that a signal interrupted.
func flock(fd, how int) error {
	for {
		err := unix.Flock(fd, how)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
