package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fixture(t *testing.T, mode string) (string, string, string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "c1-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	dest, data := filepath.Join(root, "target"), filepath.Join(root, "var")
	if err := os.MkdirAll(filepath.Join(dest, "daemon/payload"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	control, child := os.Getenv("DAEMON_CONTROL_BIN"), os.Getenv("DAEMON_CHILD_BIN")
	if control == "" {
		control, child = filepath.Join(root, "control"), filepath.Join(root, "child")
		for _, spec := range [][]string{{"build", "-o", control, "."}, {"build", "-o", child, "./testdata/child.go"}} {
			if raw, err := exec.Command("go", spec...).CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, raw)
			}
		}
	}
	raw, err := os.ReadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dest, "daemon/payload/app")
	if err := os.WriteFile(app, raw, 0755); err != nil {
		t.Fatal(err)
	}
	// macOS verifies a newly written executable on its first exec, which takes
	// seconds on a loaded host; pay it here, not inside a one-second budget.
	if raw, err := exec.Command(app, "cleanup").CombinedOutput(); err != nil {
		t.Fatalf("payload warm-up: %v %s", err, raw)
	}
	cfg := config{Package: "DaemonFixture", Exec: "app", Args: []string{"${SYNOPKG_PKGVAR}/a space", "$(touch BAD)'", "${SYNOPKG_PKGDEST}"}, Env: map[string]string{"APP_MODE": mode}, Timeouts: deadlines{Start: 2, Stop: 2, Status: 1, Uninstall: 2}}
	if os.Geteuid() == 0 {
		cfg.Package = "nobody"
	} else {
		u, err := user.Current()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Package = u.Username
	}
	raw, _ = json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dest, "daemon/config.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		// Only inside the disposable root VM: run the shipped manager as nobody.
		if runtime.GOOS != "linux" {
			t.Fatal("root fixture requires Linux VM")
		}
		filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
			if err == nil {
				return os.Chown(path, 65534, 65534)
			}
			return err
		})
		os.Chmod(root, 0755)
	}
	t.Cleanup(func() { runControl(control, "stop", dest, data) })
	return control, dest, data
}

func runControl(control, action, dest, data string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, control, action, dest, data)
	cmd.Env = append(os.Environ(), "UNLISTED_SECRET=must-not-leak", "LD_PRELOAD=must-not-leak")
	dropFixtureUser(cmd)
	raw, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(raw)
	}
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode(), string(raw)
	}
	return -1, err.Error()
}

// Readiness only proves the child stayed alive for a second, not that it reached
// main: on a loaded host a stop can kill it before it writes its line. Spawn
// identity therefore comes from the inventory, and log content is awaited.
func recordedChild(t *testing.T, data string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(data, "daemon/daemon.pid"))
	if err != nil {
		t.Fatal(err)
	}
	var r record
	if err := json.Unmarshal(raw, &r); err != nil || r.Child < 1 {
		t.Fatalf("daemon inventory: %v %s", err, raw)
	}
	return r.Child
}

func awaitChildLines(t *testing.T, data string, want int) {
	t.Helper()
	path := filepath.Join(data, "daemon/daemon.log")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		raw, _ := os.ReadFile(path)
		if got := strings.Count(string(raw), `"uid":`); got >= want || time.Now().After(deadline) {
			if got != want {
				t.Fatalf("child lines=%d want=%d: %s", got, want, raw)
			}
			return
		}
	}
}

func TestDaemonLifecycle(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	step := func(action string, want int) {
		t.Helper()
		if rc, out := runControl(control, action, dest, data); rc != want {
			t.Fatalf("%s rc=%d want=%d: %s", action, rc, want, out)
		}
	}
	step("status", 3)
	step("stop", 0)
	step("start", 0)
	awaitChildLines(t, data, 1)
	first := recordedChild(t, data)
	step("status", 0)
	step("start", 0)
	if child := recordedChild(t, data); child != first {
		t.Fatalf("duplicate start spawned another child: %d replaced %d", child, first)
	}
	step("restart", 0)
	if child := recordedChild(t, data); child == first {
		t.Fatalf("restart kept child %d", child)
	}
	awaitChildLines(t, data, 2)
	step("stop", 0)
	step("stop", 0)
	step("status", 3)
	step("unknown", 1)
	raw, err := os.ReadFile(filepath.Join(data, "daemon/daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"uid":`) != 2 {
		t.Fatalf("unexpected child spawn: %s", raw)
	}
	if strings.Contains(string(raw), "must-not-leak") {
		t.Fatalf("ambient environment leaked: %s", raw)
	}
	if !strings.Contains(string(raw), "$(touch BAD)'") || !strings.Contains(string(raw), "/a space") {
		t.Fatalf("argv changed: %s", raw)
	}
	stat, _ := os.Stat(filepath.Join(data, "daemon/daemon.log"))
	if stat.Mode().Perm() != 0600 {
		t.Fatal("log permissions")
	}
	stat, err = os.Stat(filepath.Join(data, "daemon/tmp"))
	if err != nil || stat.Mode().Perm() != 0700 {
		t.Fatal("private TMPDIR missing", err)
	}
}

func TestImmediateExitAndEscalation(t *testing.T) {
	for _, mode := range []string{"exit", "ignore"} {
		t.Run(mode, func(t *testing.T) {
			control, dest, data := fixture(t, mode)
			rc, out := runControl(control, "start", dest, data)
			if mode == "exit" {
				if rc != 0 {
					t.Fatalf("supervisor was not admitted: %d %s", rc, out)
				}
				if status, _ := runControl(control, "status", dest, data); status == 0 {
					t.Fatal("early crash reported ready")
				}
				if rc, out = runControl(control, "stop", dest, data); rc != 0 {
					t.Fatalf("stop retries: %d %s", rc, out)
				}
			} else {
				if rc != 0 {
					t.Fatalf("start: %d %s", rc, out)
				}
				if rc, out = runControl(control, "stop", dest, data); rc != 0 {
					t.Fatalf("escalation: %d %s", rc, out)
				}
			}
			if rc, out = runControl(control, "status", dest, data); rc != 3 {
				t.Fatalf("residue: %d %s", rc, out)
			}
		})
	}
}

func TestForeignStateAndRootRefusal(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	if os.Geteuid() == 0 {
		raw, err := exec.Command(control, "start", dest, data).CombinedOutput()
		if err == nil || !strings.Contains(string(raw), "package account") {
			t.Fatalf("root accepted: %s %v", raw, err)
		}
	}
	os.Mkdir(filepath.Join(data, "daemon"), 0700)
	if os.Geteuid() == 0 {
		os.Chown(filepath.Join(data, "daemon"), 65534, 65534)
	}
	os.Symlink(filepath.Join(data, "victim"), filepath.Join(data, "daemon/daemon.log"))
	if rc, out := runControl(control, "start", dest, data); rc != 1 {
		t.Fatalf("symlink accepted: %d %s", rc, out)
	}
	if _, err := os.Stat(filepath.Join(data, "victim")); !os.IsNotExist(err) {
		t.Fatal("followed log symlink")
	}
}

func TestUninstallCleanup(t *testing.T) {
	for _, mode := range []string{"cleanup", "cleanup-fail", "cleanup-hang"} {
		t.Run(mode, func(t *testing.T) {
			control, dest, data := fixture(t, "normal")
			path := filepath.Join(dest, "daemon/config.json")
			raw, _ := os.ReadFile(path)
			var cfg config
			json.Unmarshal(raw, &cfg)
			cfg.UninstallArgs = []string{mode, "${SYNOPKG_PKGVAR}"}
			raw, _ = json.Marshal(cfg)
			os.WriteFile(path, raw, 0644)
			if rc, out := runControl(control, "start", dest, data); rc != 0 {
				t.Fatalf("start: %d %s", rc, out)
			}
			want := 1
			if mode == "cleanup" {
				want = 0
			}
			if rc, out := runControl(control, "uninstall", dest, data); rc != want {
				t.Fatalf("cleanup rc=%d want=%d: %s", rc, want, out)
			}
			if rc, out := runControl(control, "status", dest, data); rc != 3 {
				t.Fatalf("cleanup must not restart daemon: %d %s", rc, out)
			}
		})
	}
}

// A revoke on uninstall can outlast the stop deadline on a loaded NAS; it has
// its own budget instead of Stop-1.
func TestUninstallCommandHasItsOwnBudget(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	path := filepath.Join(dest, "daemon/config.json")
	raw, _ := os.ReadFile(path)
	var cfg config
	json.Unmarshal(raw, &cfg)
	cfg.UninstallArgs = []string{"cleanup-slow"}
	cfg.Timeouts.Uninstall = 5
	raw, _ = json.Marshal(cfg)
	os.WriteFile(path, raw, 0644)
	if rc, out := runControl(control, "start", dest, data); rc != 0 {
		t.Fatalf("start: %d %s", rc, out)
	}
	if rc, out := runControl(control, "uninstall", dest, data); rc != 0 {
		t.Fatalf("slow uninstall within its budget failed: %d %s", rc, out)
	}
}

func TestPackageAccountIdentity(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	path := filepath.Join(dest, "daemon/config.json")
	raw, _ := os.ReadFile(path)
	var cfg config
	json.Unmarshal(raw, &cfg)
	cfg.Package = "root"
	raw, _ = json.Marshal(cfg)
	os.WriteFile(path, raw, 0644)
	rc, out := runControl(control, "start", dest, data)
	if rc == 0 {
		runControl(control, "stop", dest, data)
	}
	if rc != 1 {
		t.Fatalf("other account accepted: %d %s", rc, out)
	}
}

func TestFIFOInventoryCannotBlockStatus(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	state := filepath.Join(data, "daemon")
	os.Mkdir(state, 0700)
	path := filepath.Join(state, "daemon.pid")
	t.Cleanup(func() { os.Remove(path) })
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		os.Chown(state, 65534, 65534)
		os.Chown(path, 65534, 65534)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, control, "status", dest, data)
	dropFixtureUser(cmd)
	raw, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("status blocked on FIFO")
	}
	if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 1 {
		t.Fatalf("FIFO status: %s %v", raw, err)
	}
}

// A backend daemon runs the App's installed ui/cgi/api.cgi with its args.
func TestBackendDaemonRunsInstalledBackend(t *testing.T) {
	control, dest, data := fixture(t, "normal")
	path := filepath.Join(dest, "daemon/config.json")
	raw, _ := os.ReadFile(path)
	var cfg config
	json.Unmarshal(raw, &cfg)
	app, _ := os.ReadFile(filepath.Join(dest, "daemon/payload/app"))
	os.MkdirAll(filepath.Join(dest, "ui/cgi"), 0755)
	if err := os.WriteFile(filepath.Join(dest, "ui/cgi/api.cgi"), app, 0755); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dest, "daemon/payload/app"))
	cfg.Exec, cfg.Backend = "", true
	raw, _ = json.Marshal(cfg)
	os.WriteFile(path, raw, 0644)
	if rc, out := runControl(control, "start", dest, data); rc != 0 {
		t.Fatalf("start: %d %s", rc, out)
	}
	if rc, out := runControl(control, "status", dest, data); rc != 0 {
		t.Fatalf("status: %d %s", rc, out)
	}
	if rc, out := runControl(control, "stop", dest, data); rc != 0 {
		t.Fatalf("stop: %d %s", rc, out)
	}
	cfg.Exec = "app"
	raw, _ = json.Marshal(cfg)
	os.WriteFile(path, raw, 0644)
	if rc, out := runControl(control, "start", dest, data); rc != 1 {
		runControl(control, "stop", dest, data)
		t.Fatalf("backend with another executable accepted: %d %s", rc, out)
	}
}
