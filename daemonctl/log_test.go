package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDaemonLogRotatesWithinLimit(t *testing.T) {
	logs := newDaemonLogFiles(t.TempDir(), 32)
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
	if _, err := os.Stat(logs.previous); !os.IsNotExist(err) {
		t.Fatalf("archive before the limit was crossed: %v", err)
	}
	// A reader that remains open across rotation still sees its old inode.
	reader, err := os.Open(logs.current)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	write('b', 20)
	check(logs.current, 'b', 20)
	check(logs.previous, 'a', 32)
	write('c', 20)
	check(logs.current, 'c', 20)
	check(logs.previous, 'b', 20)
	raw := make([]byte, 32)
	if _, err := reader.ReadAt(raw, 0); err != nil || !bytes.Equal(raw, bytes.Repeat([]byte{'a'}, 32)) {
		t.Fatalf("open reader changed after rotation: %q %v", raw, err)
	}
}

func TestDaemonLogRejectsUnsafeFiles(t *testing.T) {
	for _, name := range []string{daemonLogName, daemonLogName + ".1"} {
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
				logs := newDaemonLogFiles(dir, 32)
				if err := checkDaemonLogs(dir); err == nil {
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
	dir := filepath.Join(data, "daemon")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, daemonLogName)
	marker := []byte("last pre-upgrade line\n")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(marker, daemonLogLimit+32)
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
	var snapshot record
	if err := json.Unmarshal([]byte(out), &snapshot); rc != 0 || err != nil || snapshot.Log != daemonLogPath {
		t.Fatalf("status-json: rc=%d err=%v output=%q", rc, err, out)
	}
	if rc, out := runControl(control, "stop", dest, data); rc != 0 {
		t.Fatalf("stop: %d %s", rc, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(raw, []byte("fixture stderr")) {
		t.Fatalf("current stderr missing: %q %v", raw, err)
	}
	raw, err = os.ReadFile(path + ".1")
	if err != nil || len(raw) != daemonLogLimit || !bytes.HasSuffix(raw, marker) {
		t.Fatalf("archive did not retain the legacy tail: size=%d err=%v", len(raw), err)
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
	if rc, out := runControl(control, "start", dest, data); rc != 1 || !strings.Contains(out, "inspect daemon/daemon.log") {
		t.Fatalf("startup should point to diagnostics: %d %s", rc, out)
	}
	raw, err = os.ReadFile(filepath.Join(data, daemonLogPath))
	if err != nil || !strings.Contains(string(raw), "supervisor error: DAEMON_PERMANENT_ERROR") {
		t.Fatalf("missing startup failure in daemon log: %q %v", raw, err)
	}
}
