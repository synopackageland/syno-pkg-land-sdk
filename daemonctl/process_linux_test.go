//go:build linux

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

func waitDead(t *testing.T, pid int, birth string) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		if !recordedChildAlive(pid, birth) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("PID %d remained alive", pid)
}
func readFixtureRecord(t *testing.T, data string) record {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(data, "daemon/daemon.pid"))
	if err != nil {
		t.Fatal(err)
	}
	var r record
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLinuxSupervisorDeathAndConcurrentStart(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	var wg sync.WaitGroup
	results := make(chan int, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); rc, _ := runControl(control, "start", dest, data); results <- rc }()
	}
	wg.Wait()
	close(results)
	for rc := range results {
		if rc != 0 {
			t.Fatalf("concurrent start: %d", rc)
		}
	}
	r := readFixtureRecord(t, data)
	if err := syscall.Kill(r.Supervisor, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitDead(t, r.Child, r.Birth)
	if rc, out := runControl(control, "status", dest, data); rc != 1 {
		t.Fatalf("lost-supervisor status: %d %s", rc, out)
	}
	if rc, out := runControl(control, "stop", dest, data); rc != 0 {
		t.Fatalf("retire dead child: %d %s", rc, out)
	}
	if rc, out := runControl(control, "start", dest, data); rc != 0 {
		t.Fatalf("restart after death: %d %s", rc, out)
	}
	if r2 := readFixtureRecord(t, data); r2.Child == r.Child {
		t.Fatal("did not start fresh child")
	}
}

func TestLinuxInventoryNeverSignalsForeignPID(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	if rc, out := runControl(control, "start", dest, data); rc != 0 {
		t.Fatalf("start: %d %s", rc, out)
	}
	r := readFixtureRecord(t, data)
	if rc, out := runControl(control, "stop", dest, data); rc != 0 {
		t.Fatalf("stop: %d %s", rc, out)
	}
	foreign := exec.Command("/bin/sleep", "30")
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { foreign.Process.Kill(); foreign.Wait() }()
	r.Supervisor = foreign.Process.Pid
	r.Child = foreign.Process.Pid
	r.Birth = processBirth(r.Child)
	raw, _ := json.Marshal(r)
	path := filepath.Join(data, "daemon/daemon.pid")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		os.Chown(path, 65534, 65534)
	}
	for _, action := range []string{"stop", "start", "restart"} {
		if rc, out := runControl(control, action, dest, data); rc != 1 {
			t.Fatalf("%s accepted live unwitnessed PID: %d %s", action, rc, out)
		}
		if !recordedChildAlive(r.Child, r.Birth) {
			t.Fatal("signalled inventory PID")
		}
	}
	// A reused numeric PID with a different birth can be retired without killing it.
	r.Birth = "1"
	raw, _ = json.Marshal(r)
	os.WriteFile(path, raw, 0600)
	if rc, out := runControl(control, "stop", dest, data); rc != 0 {
		t.Fatalf("retire reused PID: %d %s", rc, out)
	}
	if !recordedChildAlive(foreign.Process.Pid, processBirth(foreign.Process.Pid)) {
		t.Fatal("killed reused PID")
	}
}

func TestLinuxChildSelfExitAndDeadlines(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	if rc, out := runControl(control, "start", dest, data); rc != 0 {
		t.Fatalf("start: %d %s", rc, out)
	}
	r := readFixtureRecord(t, data)
	if err := syscall.Kill(r.Child, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitDead(t, r.Child, r.Birth)
	time.Sleep(100 * time.Millisecond)
	if rc, out := runControl(control, "status", dest, data); rc != 1 {
		t.Fatalf("self exit: %d %s", rc, out)
	}
	control, dest, data = fixture(t, "ignore")
	if rc, out := runControl(control, "start", dest, data); rc != 0 {
		t.Fatalf("start: %d %s", rc, out)
	}
	r = readFixtureRecord(t, data)
	began := time.Now()
	if rc, out := runControl(control, "stop", dest, data); rc != 0 {
		t.Fatalf("stop: %d %s", rc, out)
	}
	if elapsed := time.Since(began); elapsed < time.Second || elapsed > 3*time.Second {
		t.Fatalf("stop deadline: %s", elapsed)
	}
	waitDead(t, r.Child, r.Birth)
}

func TestParseLinuxProcBirth(t *testing.T) {
	fields := []string{"S", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15", "16", "17", "18", "1234"}
	raw := "42 (a ) tricky name)"
	for _, f := range fields {
		raw += " " + f
	}
	if birth, alive := parseBirth(raw); birth != "1234" || !alive {
		t.Fatal(birth, alive)
	}
	if recordedChildAlive(os.Getpid(), strconv.Itoa(os.Getpid())) {
		t.Fatal("different process birth matched")
	}
}
