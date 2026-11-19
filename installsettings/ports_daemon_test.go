package installsettings

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestChangePortsPreservesRunningDaemonInventoryAndRestartListener(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("package account required")
	}
	// Keep the supervisor's Unix socket within the host path-length limit.
	work, err := os.MkdirTemp("/tmp", "ports-daemon-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(work) })
	root, state := filepath.Join(work, "target"), filepath.Join(work, "var")
	for _, dir := range []string{state, filepath.Join(root, "install"), filepath.Join(root, "sso")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if !packagePattern.MatchString(account.Username) {
		t.Skip("fixture requires a package-compatible account name")
	}
	if err := os.MkdirAll(filepath.Join(root, "daemon/payload"), 0755); err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(t.TempDir(), "control")
	for _, args := range [][]string{
		{"build", "-o", control, "../daemonctl"},
		{"build", "-o", filepath.Join(root, "daemon/payload/listener"), "./testdata/listener.go"},
	} {
		if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
			t.Fatalf("build: %v: %s", err, out)
		}
	}
	// Ask the OS for distinct free ports and release them before starting the
	// actual foreground listener under the shipped supervisor.
	listeners := make([]net.Listener, 2)
	ports := make([]int, 2)
	for i := range listeners {
		listeners[i], err = net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listeners[i].Close() })
		ports[i] = listeners[i].Addr().(*net.TCPAddr).Port
	}
	oldPort, newPort := ports[0], ports[1]
	for _, l := range listeners {
		l.Close()
	}
	cfg := PortConfig{Schema: 1, Package: account.Username, Ports: map[string]Port{
		"gateway_origin": {Default: oldPort, Protocol: "tcp"},
	}, Files: map[string]string{
		"daemon/config.json":           `{"package":` + strconv.Quote(account.Username) + `,"exec":"listener","args":[],"env":{"SYNOPKGLAND_PORT_GATEWAY_ORIGIN":"@@PORT_gateway_origin@@"},"timeouts":{"start":10,"stop":5,"status":2,"uninstall":5}}`,
		"sso/native-origin-nginx.conf": "listen @@PORT_gateway_origin@@ ssl;",
	}}
	write := func(path string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(cfg)
	write("install/ports.json", raw)
	write("install/"+cfg.Package+".sc", []byte("placeholder"))
	for path := range cfg.Files {
		write(path, []byte("placeholder"))
	}
	if err := preparePorts(root, cfg.Package, state, availablePort); err != nil {
		t.Fatal(err)
	}
	if err := RememberPorts(root, cfg.Package, state); err != nil {
		t.Fatal(err)
	}
	run := func(action string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, control, action, root, state).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, out)
		}
		return nil
	}
	before, err := os.ReadFile(filepath.Join(root, "daemon/config.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Even a regression that mutates the inventory must leave no fixture child.
	t.Cleanup(func() {
		os.WriteFile(filepath.Join(root, "daemon/config.json"), before, 0600)
		if err := run("stop"); err != nil {
			t.Error(err)
		}
	})
	assertListener := func() {
		t.Helper()
		conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(oldPort)), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil || strings.TrimSpace(line) != strconv.Itoa(oldPort) {
			t.Fatalf("listener: %q, %v", line, err)
		}
	}
	if err := run("start"); err != nil {
		t.Fatal(err)
	}
	assertListener()
	if _, err := changePorts(root, cfg.Package, state, map[string]int{"gateway_origin": newPort}, availablePort); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(root, "daemon/config.json"))
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatalf("running daemon digest changed: %v", err)
	}
	status, err := ReadPortStatus(root, cfg.Package, state)
	if err != nil || status["gateway_origin"] != (PortStatus{Configured: newPort, Effective: oldPort, Pending: true}) {
		t.Fatal(status, err)
	}
	for _, action := range []string{"status", "stop", "start", "status"} {
		if err := run(action); err != nil {
			t.Fatalf("%s after change: %v", action, err)
		}
	}
	assertListener()
}
