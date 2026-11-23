package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDaemonLogRotatesWithinLimit(t *testing.T) {
	logs := newDaemonLogFiles(t.TempDir(), 32, daemonLogKeep)
	defer logs.close()
	write := func(b byte, count int) {
		t.Helper()
		if n, err := logs.Write(bytes.Repeat([]byte{b}, count)); n != count || err != nil {
			t.Fatalf("write %c: n=%d err=%v", b, n, err)
		}
	}
	check := func(path string, b byte, count int) {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, bytes.Repeat([]byte{b}, count)) {
			t.Fatalf("%s: %q %v", path, raw, err)
		}
		f, err := openDaemonFile(path, os.O_RDONLY)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}

	write('a', 31)
	write('a', 1)
	check(logs.current, 'a', 32)
	if _, err := os.Stat(archivePath(logs.current, 1)); !os.IsNotExist(err) {
		t.Fatalf("archive before the limit was crossed: %v", err)
	}
	// A reader that remains open across rotation still sees its old inode.
	reader, err := os.Open(logs.current)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for b := byte('b'); b <= 'i'; b++ {
		write(b, 32)
		if err := logs.compressArchive(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
	}
	check(logs.current, 'i', 32)
	for i := 1; i <= daemonLogKeep; i++ {
		path := archivePath(logs.current, i)
		if raw := readGzip(t, path+".gz"); !bytes.Equal(raw, bytes.Repeat([]byte{'i' - byte(i)}, 32)) {
			t.Fatalf("%s.gz: %q", path, raw)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("uncompressed source remains: %v", err)
		}
	}
	if _, err := os.Stat(archivePath(logs.current, daemonLogKeep+1) + ".gz"); !os.IsNotExist(err) {
		t.Fatalf("archive exceeded retention: %v", err)
	}
	raw := make([]byte, 32)
	if _, err := reader.ReadAt(raw, 0); err != nil || !bytes.Equal(raw, bytes.Repeat([]byte{'a'}, 32)) {
		t.Fatalf("open reader changed after rotation: %q %v", raw, err)
	}
}

func readGzip(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDaemonLogCompressFailureRetryAndPrune(t *testing.T) {
	logs := newDaemonLogFiles(t.TempDir(), 32, 3)
	defer logs.close()
	raw := archivePath(logs.current, 1)
	legacy := bytes.Repeat([]byte("old-to-new"), 5)
	obsolete := []string{archivePath(logs.current, 4), archivePath(logs.current, 7) + ".gz", logs.compressionTemp()}
	for _, path := range append(obsolete, raw) {
		if err := os.WriteFile(path, legacy, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := logs.open(); err != nil {
		t.Fatal(err)
	}
	for _, path := range obsolete {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be pruned: %v", path, err)
		}
	}
	// Obstruct publication after the worker has written a complete gzip.
	if err := os.Mkdir(raw+".gz", 0700); err != nil {
		t.Fatal(err)
	}
	if err := logs.compressArchive(t.Context(), 1); err == nil {
		t.Fatal("compression unexpectedly published to a directory")
	}
	data, err := os.ReadFile(raw)
	if err != nil || !bytes.Equal(data, legacy) {
		t.Fatalf("compression failure changed the original: %q %v", data, err)
	}
	if err := os.Remove(raw + ".gz"); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Write(bytes.Repeat([]byte{'a'}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	// Retry after rotation moved the original pending archive to .2.
	moved := archivePath(logs.current, 2)
	if err := logs.compressArchive(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	if data := readGzip(t, moved+".gz"); !bytes.Equal(data, legacy[len(legacy)-32:]) {
		t.Fatalf("legacy tail: %q", data)
	}
}

func TestDaemonLogPublishesToMovedArchive(t *testing.T) {
	logs := newDaemonLogFiles(t.TempDir(), 32, 3)
	defer logs.close()
	if err := logs.open(); err != nil {
		t.Fatal(err)
	}
	first := archivePath(logs.current, 1)
	if err := os.WriteFile(first, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := daemonLogInfo(first)
	if err != nil {
		t.Fatal(err)
	}
	tmp := logs.compressionTemp()
	if err := os.WriteFile(tmp, []byte("temp gzip"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{bytes.Repeat([]byte{'a'}, 32), []byte("b")} {
		if _, err := logs.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := logs.publishArchive(t.Context(), tmp, info, 1); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(first); err != nil || !bytes.Equal(content, bytes.Repeat([]byte{'a'}, 32)) {
		t.Fatalf("publication touched the new .1: %q %v", content, err)
	}
	if content, err := os.ReadFile(archivePath(logs.current, 2) + ".gz"); err != nil || string(content) != "temp gzip" {
		t.Fatalf("publication missed the moved .2: %q %v", content, err)
	}
}

func TestDaemonLogRejectsUnsafeFiles(t *testing.T) {
	for _, name := range []string{daemonLogName, daemonLogName + ".1", daemonLogName + ".7.gz", daemonLogName + ".gz.tmp"} {
		for _, kind := range []string{"symlink", "hardlink", "permissions", "fifo"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, name)
				victim := filepath.Join(dir, "victim")
				if err := os.WriteFile(victim, []byte("untouched"), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(victim, path)
				case "hardlink":
					err = os.Link(victim, path)
				case "permissions":
					err = os.WriteFile(path, nil, 0644)
				case "fifo":
					err = syscall.Mkfifo(path, 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				logs := newDaemonLogFiles(dir, 32, daemonLogKeep)
				if err := logs.check(); err == nil {
					t.Fatal("startup accepted unsafe log")
				}
				if err := logs.open(); err == nil {
					logs.close()
					t.Fatal("writer accepted unsafe log")
				}
				if raw, err := os.ReadFile(victim); err != nil || string(raw) != "untouched" {
					t.Fatalf("victim changed: %q %v", raw, err)
				}
			})
		}
	}
}

func TestDaemonLogCaptureDrainsChildWhileDiskWriteStalls(t *testing.T) {
	log, err := newDaemonLogCapture()
	if err != nil {
		t.Fatal(err)
	}
	sink := &stalledLogSink{started: make(chan struct{}), resume: make(chan struct{})}
	go func() {
		defer close(log.done)
		log.collect(sink)
	}()
	defer func() {
		close(sink.resume)
		log.finish(10*time.Second, errors.New("startup diagnosis"))
		if !strings.Contains(sink.String(), "DAEMON_LOG_DROPPED bytes=") {
			t.Error("missing drop count after a failed disk write")
		}
		if !strings.Contains(sink.String(), "supervisor error: startup diagnosis") {
			t.Error("missing final supervisor error after a full queue")
		}
	}()
	log.Write([]byte("initial\n"))
	select {
	case <-sink.started:
	case <-time.After(10 * time.Second):
		t.Fatal("writer never reached the simulated disk stall")
	}
	// The helper writes substantially more than the capture queue and kernel
	// pipe can hold. It must exit normally while the simulated disk is stuck.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "head -c 4194304 /dev/zero && printf 'stderr marker\\n' >&2")
	cmd.Stdout, cmd.Stderr = log.output, log.output
	if err := cmd.Run(); err != nil {
		t.Fatalf("child could not finish logging while the disk was stalled: %v", err)
	}
	if n := log.dropped.Load(); n == 0 {
		t.Fatal("full queue did not drop output")
	}
}

type stalledLogSink struct {
	bytes.Buffer
	started, resume chan struct{}
}

func (s *stalledLogSink) Write(p []byte) (int, error) {
	if s.started != nil {
		close(s.started)
		<-s.resume
		s.started = nil
		return 0, syscall.ENOSPC
	}
	return s.Buffer.Write(p)
}

func TestDaemonLogUpgradeAndStatus(t *testing.T) {
	control, dest, data := fixture(t, "streams")
	configPath := filepath.Join(dest, "daemon/config.json")
	cfg, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var options config
	if err := json.Unmarshal(cfg, &options); err != nil {
		t.Fatal(err)
	}
	maxMB, keep := 1, 3
	options.LogMaxMB, options.LogKeepFiles = &maxMB, &keep
	cfg, err = json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, cfg, 0644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(data, "daemon")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, daemonLogName)
	limit := int64(maxMB) << 20
	marker := []byte("last pre-upgrade line\n")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(marker, limit+32)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		for _, name := range []string{dir, path} {
			if err := os.Chown(name, 65534, 65534); err != nil {
				t.Fatal(err)
			}
		}
	}
	if rc, out := runControl(control, "start", dest, data); rc != 0 {
		t.Fatalf("start: %d %s", rc, out)
	}
	awaitChildLines(t, data, 1)
	rc, out := runControl(control, "status-json", dest, data)
	var snapshot logStatusRecord
	if err := json.Unmarshal([]byte(out), &snapshot); rc != 0 || err != nil || snapshot.Log != daemonLogPath ||
		strings.Join(snapshot.LogArchives, ",") != "daemon/daemon.log.1.gz,daemon/daemon.log.2.gz,daemon/daemon.log.3.gz" {
		t.Fatalf("status-json: rc=%d err=%v output=%q", rc, err, out)
	}
	archive := path + ".1.gz"
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(archive); err == nil {
			break
		} else if !os.IsNotExist(err) || time.Now().After(deadline) {
			t.Fatalf("archive not compressed by the running supervisor: %v", err)
		}
	}
	if rc, out := runControl(control, "stop", dest, data); rc != 0 {
		t.Fatalf("stop: %d %s", rc, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(raw, []byte("fixture stderr")) {
		t.Fatalf("current stderr missing: %q %v", raw, err)
	}
	raw = readGzip(t, archive)
	if int64(len(raw)) != limit || !bytes.HasSuffix(raw, marker) {
		t.Fatalf("archive did not retain the legacy tail: size=%d", len(raw))
	}
}

func TestDaemonLogPolicy(t *testing.T) {
	if limit, keep, err := (config{}).logPolicy(); limit != 100<<20 || keep != 7 || err != nil {
		t.Fatalf("default policy: %d bytes, %d archives, %v", limit, keep, err)
	}
	maxMB, maxKeep := 2048, 20
	if limit, keep, err := (config{LogMaxMB: &maxMB, LogKeepFiles: &maxKeep}).logPolicy(); limit != 2048<<20 || keep != 20 || err != nil {
		t.Fatalf("maximum policy: %d bytes, %d archives, %v", limit, keep, err)
	}
	for _, n := range []int{-1, 0, 2049} {
		if _, _, err := (config{LogMaxMB: &n}).logPolicy(); err == nil {
			t.Fatalf("accepted log_max_mb: %d", n)
		}
	}
	for _, n := range []int{-1, 0, 21} {
		if _, _, err := (config{LogKeepFiles: &n}).logPolicy(); err == nil {
			t.Fatalf("accepted log_keep_files: %d", n)
		}
	}
}

func TestDaemonLogStartupFailure(t *testing.T) {
	control, dest, data := fixture(t, "exit")
	path := filepath.Join(dest, "daemon/config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Restart = &restartPolicy{Mode: "off"}
	raw, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if rc, out := runControl(control, "start", dest, data); rc != 1 ||
		!strings.Contains(out, "inspect daemon/daemon.log and daemon/daemon.log.1.gz through daemon/daemon.log.7.gz") {
		t.Fatalf("startup should point to diagnostics: %d %s", rc, out)
	}
	raw, err = os.ReadFile(filepath.Join(data, daemonLogPath))
	if err != nil || !strings.Contains(string(raw), "supervisor error: DAEMON_PERMANENT_ERROR") {
		t.Fatalf("missing startup failure in daemon log: %q %v", raw, err)
	}
}
