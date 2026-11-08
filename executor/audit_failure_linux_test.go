//go:build linux

package main

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAuditPartialAppendCanRetryWithoutCorruptingJSONL(t *testing.T) {
	if os.Getenv("SDK_AUDIT_SHORT_WRITE") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAuditPartialAppendCanRetryWithoutCorruptingJSONL$")
		cmd.Env = append(os.Environ(), "SDK_AUDIT_SHORT_WRITE=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("short-write probe: %v\n%s", err, out)
		}
		return
	}
	s := &Store{Dir: t.TempDir()}
	if err := s.Audit(auditEvent(1)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(s.Dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AuditReadOnly(auditEvent(2)); err != nil {
		t.Fatal(err)
	}
	var saved syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &saved); err != nil {
		t.Fatal(err)
	}
	limited := saved
	limited.Cur = uint64(len(before) + 10)
	signal.Ignore(syscall.SIGXFSZ)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
		t.Fatal(err)
	}
	err = s.FlushAudit()
	if restore := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &saved); restore != nil {
		t.Fatal(restore)
	}
	if err == nil {
		t.Fatal("short write accepted")
	}
	after, err := os.ReadFile(filepath.Join(s.Dir, "audit.jsonl"))
	if err != nil || string(after) != string(before) {
		t.Fatal("failed batch left truncated/corrupt JSONL", err)
	}
	if err := s.FlushAudit(); err != nil {
		t.Fatal(err)
	}
	if events := auditEvents(t, s); len(events) != 2 || events[1].Result != "2" {
		t.Fatal("retry lost or duplicated pending audit", events)
	}
}
