package main

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"time"
)

func (l *daemonLogFiles) scheduleCompression() {
	select {
	case l.compress <- struct{}{}:
	default:
	}
}

func (l *daemonLogFiles) compressLoop(ctx context.Context) {
	lowPriorityCompression()
	// Wait until the disk worker has recovered the archive directory.
	select {
	case <-ctx.Done():
		return
	case <-l.compress:
	}
	retry := time.NewTicker(time.Minute)
	defer retry.Stop()
	for {
		for i := 1; i <= l.keep && ctx.Err() == nil; i++ {
			// Leave the raw archive in place if any step fails. A later pass
			// retries even if log output goes quiet.
			_ = l.compressArchive(ctx, i)
		}
		select {
		case <-ctx.Done():
			return
		case <-l.compress:
		case <-retry.C:
		}
	}
}

type cancelLogReader struct {
	ctx context.Context
	io.Reader
}

func (r cancelLogReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func (l *daemonLogFiles) compressArchive(ctx context.Context, index int) error {
	// Pin this archive's inode while rotation is free to rename it.
	l.archiveMu.Lock()
	raw, err := openDaemonFile(archivePath(l.current, index), os.O_RDONLY)
	l.archiveMu.Unlock()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer raw.Close()
	st, err := raw.Stat()
	if err != nil {
		return err
	}
	tmp := l.compressionTemp()
	dst, err := openDaemonFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	defer removeLog(tmp)
	defer dst.Close()
	zip, err := gzip.NewWriterLevel(dst, gzip.BestSpeed)
	if err != nil {
		return err
	}
	// An oversized legacy archive contributes only its newest bytes. Its
	// source remains intact until the gzip has been safely published.
	tail := io.NewSectionReader(raw, max(0, st.Size()-l.limit), l.limit)
	if _, err := io.Copy(zip, cancelLogReader{ctx, tail}); err != nil {
		return err
	}
	if err := zip.Close(); err != nil {
		return err
	}
	if err := dst.Sync(); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return l.publishArchive(ctx, tmp, st, index)
}

func (l *daemonLogFiles) publishArchive(ctx context.Context, tmp string, source os.FileInfo, index int) error {
	l.archiveMu.Lock()
	defer l.archiveMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	for i := index; i <= l.keep; i++ {
		raw := archivePath(l.current, i)
		st, err := daemonLogInfo(raw)
		if err != nil {
			return err
		}
		if st == nil || !os.SameFile(st, source) {
			continue
		}
		gz := raw + ".gz"
		if _, err := daemonLogInfo(gz); err != nil {
			return err
		}
		if err := os.Rename(tmp, gz); err != nil {
			return err
		}
		// Keep the source if publishing the directory entry fails to sync.
		dir, err := os.Open(filepath.Dir(raw))
		if err != nil {
			return err
		}
		defer dir.Close()
		if err := dir.Sync(); err != nil {
			return err
		}
		return removeLog(raw)
	}
	// The source fell out of retention while compression was running.
	return nil
}
