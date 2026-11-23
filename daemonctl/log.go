package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

const (
	daemonLogPath  = "daemon/daemon.log"
	daemonLogName  = "daemon.log"
	daemonLogLimit = 4 * 1024 * 1024
	logBlockSize   = 16 * 1024
	logQueueSize   = 16
)

// A pipe drainer never does filesystem I/O, and it drops output when the
// bounded queue is full. Keep draining even if every disk write fails, so
// storage trouble neither fills the child's pipe nor closes it under the child.
type daemonLogCapture struct {
	input, output *os.File
	pumped, done  chan struct{}
	queue         chan logBlock
	dropped       atomic.Uint64
	stopBy        time.Time
	finalError    error
}

type logBlock struct {
	data [logBlockSize]byte
	size int
}

func newDaemonLogCapture() (*daemonLogCapture, error) {
	input, output, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	log := &daemonLogCapture{
		input: input, output: output, pumped: make(chan struct{}), done: make(chan struct{}),
		queue: make(chan logBlock, logQueueSize),
	}
	go log.drain()
	return log, nil
}

func (l *daemonLogCapture) drain() {
	defer close(l.pumped)
	defer l.input.Close()
	var block logBlock
	for {
		n, err := l.input.Read(block.data[:])
		if n > 0 {
			block.size = n
			l.enqueue(block)
		}
		if err != nil {
			return
		}
	}
}

func (l *daemonLogCapture) enqueue(block logBlock) {
	select {
	case l.queue <- block:
	default:
		l.dropped.Add(uint64(block.size))
	}
}

// Supervisor messages use the same queue without writing back into the pipe.
func (l *daemonLogCapture) Write(p []byte) (int, error) {
	size := len(p)
	for len(p) > 0 {
		var block logBlock
		block.size = copy(block.data[:], p)
		l.enqueue(block)
		p = p[block.size:]
	}
	return size, nil
}

func (l *daemonLogCapture) collect(dst io.Writer) {
	for block := range l.queue {
		n, _ := dst.Write(block.data[:block.size])
		if n < block.size {
			l.dropped.Add(uint64(block.size - n))
		} else {
			l.reportDropped(dst)
		}
	}
	l.reportDropped(dst)
	if l.finalError != nil {
		supervisorLog(dst, "supervisor error: %v", l.finalError)
	}
}

func (l *daemonLogCapture) reportDropped(dst io.Writer) {
	if n := l.dropped.Swap(0); n > 0 {
		if _, err := fmt.Fprintf(dst, "DAEMON_LOG_DROPPED bytes=%d\n", n); err != nil {
			l.dropped.Add(n)
		}
	}
}

// Children have stopped by this point. Use the lifecycle budget for a final
// flush, including when a descendant kept the pipe or a disk write is stuck.
func (l *daemonLogCapture) finish(budget time.Duration, err error) {
	l.output.Close()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-l.pumped:
	case <-timer.C:
		l.input.Close()
		<-l.pumped
		l.finalError = err
		close(l.queue)
		return
	}
	l.finalError = err
	close(l.queue)
	select {
	case <-l.done:
	case <-timer.C:
	}
}

// Only the lock-owning supervisor's disk worker writes these two files. It
// never passes a log file descriptor to another process.
type daemonLogFiles struct {
	current, previous string
	limit, size       int64
	file              *os.File
}

func newDaemonLogFiles(dir string, limit int64) *daemonLogFiles {
	path := filepath.Join(dir, daemonLogName)
	return &daemonLogFiles{current: path, previous: path + ".1", limit: limit}
}

// Check the existing paths before accepting startup. Missing files are fine:
// creation belongs to the disk worker and may fail temporarily, e.g. on ENOSPC.
func checkDaemonLogs(dir string) error {
	files := newDaemonLogFiles(dir, daemonLogLimit)
	for _, path := range []string{files.current, files.previous} {
		f, err := openDaemonFile(path, os.O_RDONLY)
		if err == nil {
			f.Close()
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (l *daemonLogFiles) open() error {
	previous, err := openDaemonFile(l.previous, os.O_RDWR)
	if err == nil {
		_, err = trimDaemonLog(previous, l.limit)
		previous.Close()
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := safeFile(l.current)
	if err != nil {
		return err
	}
	size, err := trimDaemonLog(f, l.limit)
	if err != nil {
		f.Close()
		return err
	}
	l.file, l.size = f, size
	return nil
}

func trimDaemonLog(f *os.File, limit int64) (int64, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if st.Size() <= limit {
		return st.Size(), nil
	}
	// Preserve the tail of an oversized legacy log. Copy toward the start
	// with bounded memory and truncate even if the in-place copy fails.
	_, err = io.Copy(io.NewOffsetWriter(f, 0), io.NewSectionReader(f, st.Size()-limit, limit))
	return limit, errors.Join(err, f.Truncate(limit))
}

func (l *daemonLogFiles) Write(p []byte) (int, error) {
	if int64(len(p)) > l.limit {
		return 0, errors.New("daemon log write exceeds file limit")
	}
	if l.file == nil {
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	if l.size+int64(len(p)) > l.limit {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := l.file.WriteAt(p, l.size)
	l.size += int64(n)
	return n, err
}

func (l *daemonLogFiles) rotate() error {
	current, err := openDaemonFile(l.current, os.O_RDONLY)
	if err != nil {
		return err
	}
	st, err := current.Stat()
	current.Close()
	if err != nil {
		return err
	}
	active, err := l.file.Stat()
	if err != nil || !os.SameFile(st, active) {
		return errors.New("daemon log changed during rotation")
	}
	previous, err := openDaemonFile(l.previous, os.O_RDONLY)
	if err == nil {
		previous.Close()
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(l.current, l.previous); err != nil {
		return err
	}
	l.close()
	return l.open()
}

func (l *daemonLogFiles) close() {
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}
