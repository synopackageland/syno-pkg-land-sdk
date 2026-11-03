//go:build linux

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDaemonNoFileFailsClosedAndChildInheritsNonRootLimit(t *testing.T) {
	control, dest, data := fixture(t, "stable")
	path := filepath.Join(dest, "daemon/config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config
	json.Unmarshal(raw, &cfg)
	cfg.NoFile = 32768
	cfg.Timeouts = deadlines{Start: 10, Stop: 5, Status: 3, Uninstall: 5}
	raw, _ = json.Marshal(cfg)
	if err = os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	run := func(limit, action string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "prlimit", "--nofile="+limit, control, action, dest, data)
		dropFixtureUser(cmd)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("4096:4096", "start"); err == nil {
		t.Fatalf("low hard limit did not refuse: %v %s", err, out)
	}
	log, err := os.ReadFile(filepath.Join(data, "daemon/daemon.log"))
	if err != nil || !strings.Contains(string(log), "DAEMON_NOFILE_INSUFFICIENT") {
		t.Fatal("missing safe limit diagnosis", err, string(log))
	}
	if out, err := run("1024:65536", "start"); err != nil {
		t.Fatalf("unit limit start: %v %s", err, out)
	}
	pid := recordedChild(t, data)
	status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(status), "Uid:\t0\t") {
		t.Fatal("root child")
	}
	limits, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/limits")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(limits), "\n") {
		if strings.HasPrefix(line, "Max open files") {
			fields := strings.Fields(line)
			soft, _ := strconv.Atoi(fields[3])
			hard, _ := strconv.Atoi(fields[4])
			if soft < 32768 || hard != 65536 {
				t.Fatal(line)
			}
		}
	}
	// Status and stop from ordinary low-limit package hooks remain available.
	for _, action := range []string{"status", "stop"} {
		if out, err := run("4096:4096", action); err != nil {
			t.Fatal(action, err, out)
		}
	}
}
