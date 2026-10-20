package idgateway

import (
	"bytes"
	"context"
	"errors"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// watchSettle coalesces the burst of writes one account change makes.
const watchSettle = 200 * time.Millisecond

// watchFile calls changed after the file name in dir is written, replaced by
// rename or removed, until ctx ends. It watches the directory with inotify,
// which needs no root: DSM replaces /etc/passwd by rename, which a watch on
// the file itself would lose.
func watchFile(ctx context.Context, dir, name string, changed func()) error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "inotify")
	if _, err := unix.InotifyAddWatch(fd, dir, unix.IN_CLOSE_WRITE|unix.IN_MOVED_TO|unix.IN_MOVED_FROM|unix.IN_CREATE|unix.IN_DELETE); err != nil {
		f.Close()
		return err
	}
	events, readErr := make(chan struct{}, 1), make(chan error, 1)
	go func() { readErr <- readEvents(f, name, events) }()
	defer f.Close()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			return err
		case <-events:
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(watchSettle):
			}
			select {
			case <-events:
			default:
			}
			changed()
		}
	}
}

// readEvents signals events for each inotify event on name, or an overflow,
// until f is closed.
func readEvents(f *os.File, name string, events chan<- struct{}) error {
	buf := make([]byte, 64<<10)
	for {
		n, err := f.Read(buf)
		if errors.Is(err, os.ErrClosed) {
			return nil
		}
		if err != nil {
			return err
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			raw := buf[off+unix.SizeofInotifyEvent : off+unix.SizeofInotifyEvent+int(ev.Len)]
			off += unix.SizeofInotifyEvent + int(ev.Len)
			if ev.Mask&unix.IN_Q_OVERFLOW != 0 || string(bytes.TrimRight(raw, "\x00")) == name {
				select {
				case events <- struct{}{}:
				default:
				}
			}
		}
	}
}
