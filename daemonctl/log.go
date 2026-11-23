package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	daemonLogPath  = "daemon/daemon.log"
	daemonLogName  = "daemon.log"
	daemonLogLimit = 100 * 1024 * 1024
	daemonLogKeep  = 7
	maxLogKeep     = 20
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

func (c config) logPolicy() (int64, int, error) {
	var limit int64 = daemonLogLimit
	if c.LogMaxMB != nil {
		if *c.LogMaxMB < 1 || *c.LogMaxMB > 2048 {
			return 0, 0, errors.New("daemon log_max_mb must be an integer from 1 to 2048 MiB")
		}
		limit = int64(*c.LogMaxMB) << 20
	}
	keep := daemonLogKeep
	if c.LogKeepFiles != nil {
		if *c.LogKeepFiles < 1 || *c.LogKeepFiles > maxLogKeep {
			return 0, 0, fmt.Errorf("daemon log_keep_files must be an integer from 1 to %d", maxLogKeep)
		}
		keep = *c.LogKeepFiles
	}
	return limit, keep, nil
}

func archivePath(base string, index int) string {
	return base + "." + strconv.Itoa(index)
}

func (c *controller) logHint() string {
	first := archivePath(daemonLogPath, 1) + ".gz"
	if c.logKeep > 1 {
		first += " through " + archivePath(daemonLogPath, c.logKeep) + ".gz"
	}
	return "inspect " + daemonLogPath + " and " + first + " (omit .gz for archives awaiting compression)"
}

type logStatusRecord struct {
	record
	LogArchives []string `json:"log_archives"`
}

func (c *controller) logStatus(r record) logStatusRecord {
	r.Log = daemonLogPath
	paths := make([]string, c.logKeep)
	for i := range paths {
		paths[i] = archivePath(daemonLogPath, i+1) + ".gz"
	}
	return logStatusRecord{record: r, LogArchives: paths}
}

// The supervisor owns all writes. The disk worker and the compressor share
// archive names under archiveMu, but compression itself runs outside the lock.
type daemonLogFiles struct {
	current     string
	limit, size int64
	keep        int
	file        *os.File
	initialized bool
	archiveMu   sync.Mutex
	compress    chan struct{}
}

func newDaemonLogFiles(dir string, limit int64, keep int) *daemonLogFiles {
	path := filepath.Join(dir, daemonLogName)
	return &daemonLogFiles{current: path, limit: limit, keep: keep, compress: make(chan struct{}, 1)}
}

func (l *daemonLogFiles) compressionTemp() string {
	return l.current + ".gz.tmp"
}

func daemonLogInfo(path string) (os.FileInfo, error) {
	f, err := openDaemonFile(path, os.O_RDONLY)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	defer f.Close()
	return f.Stat()
}

// Validate each raw and compressed archive before changing the rotation chain.
// A raw file takes precedence if a previous run stopped after publishing the
// gzip and before removing its source.
func (l *daemonLogFiles) archives(keep int) ([]string, error) {
	paths := make([]string, keep+1)
	for i := 1; i <= keep; i++ {
		raw := archivePath(l.current, i)
		for _, path := range []string{raw + ".gz", raw} {
			st, err := daemonLogInfo(path)
			if err != nil {
				return nil, err
			}
			if st != nil {
				paths[i] = path
			}
		}
	}
	return paths, nil
}

// Missing files are fine. Creation and migration belong to the disk worker.
func (l *daemonLogFiles) check() error {
	for _, path := range []string{l.current, l.compressionTemp()} {
		if _, err := daemonLogInfo(path); err != nil {
			return err
		}
	}
	_, err := l.archives(maxLogKeep)
	return err
}

func (l *daemonLogFiles) open() error {
	if !l.initialized {
		if err := l.prepareArchives(); err != nil {
			return err
		}
		l.initialized = true
		l.scheduleCompression()
	}
	return l.openCurrent()
}

func (l *daemonLogFiles) prepareArchives() error {
	l.archiveMu.Lock()
	defer l.archiveMu.Unlock()
	if err := l.check(); err != nil {
		return err
	}
	if err := removeLog(l.compressionTemp()); err != nil {
		return err
	}
	for i := l.keep + 1; i <= maxLogKeep; i++ {
		raw := archivePath(l.current, i)
		for _, path := range []string{raw, raw + ".gz"} {
			if err := removeLog(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *daemonLogFiles) openCurrent() error {
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
		if err := l.openCurrent(); err != nil {
			return 0, err
		}
	}
	n, err := l.file.WriteAt(p, l.size)
	l.size += int64(n)
	return n, err
}

func (l *daemonLogFiles) rotate() error {
	l.archiveMu.Lock()
	defer l.archiveMu.Unlock()
	st, err := daemonLogInfo(l.current)
	if err != nil {
		return err
	}
	active, err := l.file.Stat()
	if err != nil || st == nil || !os.SameFile(st, active) {
		return errors.New("daemon log changed during rotation")
	}
	paths, err := l.archives(l.keep)
	if err != nil {
		return err
	}
	for i := l.keep; i > 0; i-- {
		raw := archivePath(l.current, i)
		// A published gzip can coexist with its raw source after an interrupted
		// cleanup. Move a single authoritative copy so it cannot shadow a
		// different archive during the next compression pass.
		if paths[i] == raw || i == l.keep {
			if err := removeLog(raw + ".gz"); err != nil {
				return err
			}
		}
		if i == l.keep {
			if err := removeLog(raw); err != nil {
				return err
			}
		} else if paths[i] != "" {
			dest := archivePath(l.current, i+1)
			if paths[i] != raw {
				dest += ".gz"
			}
			if err := os.Rename(paths[i], dest); err != nil {
				return err
			}
		}
	}
	if err := os.Rename(l.current, archivePath(l.current, 1)); err != nil {
		return err
	}
	l.close()
	l.scheduleCompression()
	return nil
}

func (l *daemonLogFiles) close() {
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}

func removeLog(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (l *daemonLogFiles) collect(log *daemonLogCapture) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	defer l.close()
	defer func() {
		cancel()
		<-done
	}()
	go func() {
		defer close(done)
		l.compressLoop(ctx)
	}()
	_ = l.open()
	log.collect(l)
}
