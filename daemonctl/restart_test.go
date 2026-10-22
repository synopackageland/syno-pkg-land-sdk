package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRestartPolicyDefaultsAndValidation(t *testing.T) {
	if p, err := normalizeRestart(nil); err != nil || p != nil {
		t.Fatalf("default must remain off: %+v %v", p, err)
	}
	p, err := normalizeRestart(&restartPolicy{Mode: "on-failure"})
	if err != nil || p.InitialSeconds != 1 || p.MaxSeconds != 30 || p.MaxAttempts != 5 || p.WindowSeconds != 300 {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	for _, bad := range []restartPolicy{
		{Mode: "always"}, {}, {Mode: "on-failure", InitialSeconds: -1},
		{Mode: "on-failure", InitialSeconds: 31}, {Mode: "on-failure", MaxSeconds: 3601},
		{Mode: "on-failure", MaxAttempts: 101}, {Mode: "on-failure", WindowSeconds: 86401},
	} {
		if _, err := normalizeRestart(&bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestRestartBudgetBackoffCapAndSlidingWindow(t *testing.T) {
	p := restartPolicy{Mode: "on-failure", InitialSeconds: 1, MaxSeconds: 3, MaxAttempts: 4, WindowSeconds: 60}
	b := restartBudget{policy: &p}
	now := time.Unix(1000, 0)
	for i, want := range []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 3 * time.Second} {
		if got, ok := b.next(now.Add(time.Duration(i) * time.Second)); !ok || got != want {
			t.Fatalf("attempt %d: %s %v", i, got, ok)
		}
	}
	if _, ok := b.next(now.Add(59 * time.Second)); ok {
		t.Fatal("restart storm exceeded the cap")
	}
	if got, ok := b.next(now.Add(61 * time.Second)); !ok || got != 3*time.Second {
		t.Fatalf("sliding window: %s %v", got, ok)
	}
	if got, ok := b.next(now.Add(122 * time.Second)); !ok || got != time.Second {
		t.Fatalf("stable interval must reset backoff: %s %v", got, ok)
	}
}

func setRestartFixture(t *testing.T, dest string, p restartPolicy) {
	t.Helper()
	path := filepath.Join(dest, "daemon/config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["restart"] = p
	raw, _ = json.Marshal(cfg)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
}

func awaitRestartState(t *testing.T, data, state string, previous int) record {
	t.Helper()
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		raw, _ := os.ReadFile(filepath.Join(data, "daemon/daemon.pid"))
		var r record
		if json.Unmarshal(raw, &r) == nil && r.State == state && (previous == 0 || r.Child != previous) {
			return r
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, _ := os.ReadFile(filepath.Join(data, "daemon/daemon.log"))
	t.Fatalf("state %s not reached: %s", state, raw)
	return record{}
}

func TestCompiledRestartRecoveryAndLimit(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	setRestartFixture(t, dest, restartPolicy{Mode: "on-failure", MaxAttempts: 2, MaxSeconds: 2})
	if rc, out := runControl(control, "start", dest, data); rc != 0 {
		t.Fatalf("start: %d %s", rc, out)
	}
	r := awaitRestartState(t, data, "running", 0)
	for attempt := 1; attempt <= 3; attempt++ {
		old := r.Child
		if err := syscall.Kill(old, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		if attempt == 3 {
			break
		}
		backoff := awaitRestartState(t, data, "restarting", 0)
		if backoff.Restarts != attempt || backoff.Reason != "DAEMON_RESTART_BACKOFF" || backoff.RetryAt.IsZero() {
			t.Fatalf("backoff: %+v", backoff)
		}
		if rc, out := runControl(control, "status", dest, data); rc != 1 || !strings.Contains(out, "DAEMON_RESTART_BACKOFF") {
			t.Fatalf("backoff status: %d %s", rc, out)
		}
		if rc, out := runControl(control, "status-json", dest, data); rc != 1 || !strings.Contains(out, `"state":"restarting"`) {
			t.Fatalf("snapshot: %d %s", rc, out)
		}
		r = awaitRestartState(t, data, "running", old)
		if rc, out := runControl(control, "status", dest, data); rc != 0 {
			t.Fatalf("recovered: %d %s", rc, out)
		}
	}
	failed := awaitRestartState(t, data, "failed", 0)
	if failed.Reason != "DAEMON_RESTART_LIMIT" || failed.Restarts != 2 {
		t.Fatalf("limit: %+v", failed)
	}
	if rc, out := runControl(control, "status", dest, data); rc != 1 || !strings.Contains(out, "DAEMON_RESTART_LIMIT") {
		t.Fatalf("limit status: %d %s", rc, out)
	}
	if rc, out := runControl(control, "status-json", dest, data); rc != 1 || !strings.Contains(out, `"reason":"DAEMON_RESTART_LIMIT"`) {
		t.Fatalf("failed snapshot: %d %s", rc, out)
	}
	if rc, out := runControl(control, "stop", dest, data); rc != 0 {
		t.Fatalf("stop: %d %s", rc, out)
	}
	if rc, out := runControl(control, "status", dest, data); rc != 3 {
		t.Fatalf("stopped: %d %s", rc, out)
	}
}

func TestCompiledStopCancelsRestart(t *testing.T) {
	for _, action := range []string{"stop", "uninstall", "signal"} {
		t.Run(action, func(t *testing.T) {
			control, dest, data := fixture(t, "normal")
			setRestartFixture(t, dest, restartPolicy{Mode: "on-failure", InitialSeconds: 2})
			if rc, out := runControl(control, "start", dest, data); rc != 0 {
				t.Fatalf("start: %d %s", rc, out)
			}
			r := awaitRestartState(t, data, "running", 0)
			if err := syscall.Kill(r.Child, syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			awaitRestartState(t, data, "restarting", 0)
			if action == "signal" {
				if err := syscall.Kill(r.Supervisor, syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			} else if rc, out := runControl(control, action, dest, data); rc != 0 {
				t.Fatalf("%s: %d %s", action, rc, out)
			}
			time.Sleep(2200 * time.Millisecond)
			if rc, out := runControl(control, "status", dest, data); rc != 3 {
				t.Fatalf("resurrected: %d %s", rc, out)
			}
			awaitChildLines(t, data, 1)
		})
	}
}

func TestCompiledCleanExitDoesNotRestart(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	setRestartFixture(t, dest, restartPolicy{Mode: "on-failure"})
	if rc, out := runControl(control, "start", dest, data); rc != 0 {
		t.Fatalf("start: %d %s", rc, out)
	}
	r := awaitRestartState(t, data, "running", 0)
	awaitChildLines(t, data, 1)
	if err := syscall.Kill(r.Child, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	awaitRestartState(t, data, "exited", 0)
	time.Sleep(1200 * time.Millisecond)
	awaitChildLines(t, data, 1)
	if rc, out := runControl(control, "status", dest, data); rc != 1 {
		t.Fatalf("unexpected ready: %d %s", rc, out)
	}
}

func TestCompiledRestartFailureAndStopFence(t *testing.T) {
	for _, mode := range []string{"off", "spawn-failure", "early-crash", "stop-fence"} {
		t.Run(mode, func(t *testing.T) {
			control, dest, data := fixture(t, "normal")
			policy := restartPolicy{Mode: "on-failure", MaxAttempts: 1}
			if mode == "off" {
				policy.Mode = "off"
			}
			setRestartFixture(t, dest, policy)
			if rc, out := runControl(control, "start", dest, data); rc != 0 {
				t.Fatalf("start: %d %s", rc, out)
			}
			r := awaitRestartState(t, data, "running", 0)
			awaitChildLines(t, data, 1)
			if err := syscall.Kill(r.Child, syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			if mode == "off" {
				failed := awaitRestartState(t, data, "failed", 0)
				if failed.Restarts != 0 || failed.Reason != "DAEMON_CHILD_FAILED" {
					t.Fatalf("off: %+v", failed)
				}
				return
			}
			awaitRestartState(t, data, "restarting", 0)
			switch mode {
			case "spawn-failure":
				if err := os.Remove(filepath.Join(dest, "daemon/payload/app")); err != nil {
					t.Fatal(err)
				}
			case "early-crash":
				child := awaitRestartState(t, data, "starting", r.Child)
				if err := syscall.Kill(child.Child, syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
			case "stop-fence":
				if err := os.WriteFile(filepath.Join(data, "daemon/stop.request"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				time.Sleep(1500 * time.Millisecond)
				if rc, out := runControl(control, "status", dest, data); rc != 3 {
					t.Fatalf("fence failed: %d %s", rc, out)
				}
				awaitChildLines(t, data, 1)
				// An explicit start clears the fence and the exhausted old budget.
				if rc, out := runControl(control, "start", dest, data); rc != 0 {
					t.Fatalf("manual start: %d %s", rc, out)
				}
				return
			}
			failed := awaitRestartState(t, data, "failed", 0)
			if failed.Reason != "DAEMON_RESTART_LIMIT" || failed.Restarts != 1 {
				t.Fatalf("failure budget: %+v", failed)
			}
		})
	}
}

func TestCompiledRestartConfigValidation(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	path := filepath.Join(dest, "daemon/config.json")
	base, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, restart := range []string{`{"mode":"always"}`, `{"mode":"on-failure","max_attempts":-1}`, `{"mode":"on-failure","extra":true}`, `{"mode":"on-failure","max_attempts":1.5}`} {
		var cfg map[string]json.RawMessage
		json.Unmarshal(base, &cfg)
		cfg["restart"] = json.RawMessage(restart)
		raw, _ := json.Marshal(cfg)
		if err := os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
		if rc, out := runControl(control, "start", dest, data); rc != 1 {
			t.Fatalf("accepted %s: %d %s", restart, rc, out)
		}
	}
	// Restore the valid config so fixture cleanup remains meaningful.
	if err := os.WriteFile(path, base, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRecordUpdateFailurePreservesInventory(t *testing.T) {
	dir := t.TempDir()
	c := &controller{dir: dir}
	before := record{Supervisor: 123, Child: 456, Digest: "fixed", State: "running"}
	if err := c.writeRecord(before); err != nil {
		t.Fatal(err)
	}
	// A failed next-generation write must not destroy the old child inventory.
	if err := os.Symlink("missing", filepath.Join(dir, "daemon.next")); err != nil {
		t.Fatal(err)
	}
	if err := c.writeRecord(record{State: "restarting"}); err == nil {
		t.Fatal("accepted foreign staging file")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "daemon.pid"))
	if err != nil {
		t.Fatal(err)
	}
	var after record
	if err := json.Unmarshal(raw, &after); err != nil || after != before {
		t.Fatalf("inventory lost: %+v %v", after, err)
	}
}
