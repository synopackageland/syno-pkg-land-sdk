//go:build unix

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/bridge"
)

func TestRuntimeRefusesToStartOutsideItsPackage(t *testing.T) {
	cgi := func(name string) string {
		if name == "GATEWAY_INTERFACE" {
			return "CGI/1.1"
		}
		return ""
	}
	defer func() { packageID, manifestDigest, permissionsDigest = "", "", "" }()
	for _, c := range []struct {
		name, want string
		getenv     func(string) string
		uid        int
		digests    bool
	}{
		{"not a CGI", "web server", func(string) string { return "" }, 1000, true},
		{"unbound", "digests", cgi, 1000, false},
		{"root", "root", cgi, 0, true},
		{"no App", "no such file", cgi, 1000, true},
	} {
		packageID, manifestDigest, permissionsDigest = "", "", ""
		if c.digests {
			packageID, manifestDigest, permissionsDigest = "synopackageland-runtime-test", "00", "00"
		}
		if _, err := runtime(c.getenv, c.uid); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// --worker is for DSM's systemd user unit, never for a request: a CGI query
// without "=" can reach the command as an argument.
func TestWorkerRefusesToStartOutsideItsUnit(t *testing.T) {
	defer func() { packageID, manifestDigest, permissionsDigest = "", "", "" }()
	none := func(string) string { return "" }
	cgi := func(name string) string {
		if name == "GATEWAY_INTERFACE" {
			return "CGI/1.1"
		}
		return ""
	}
	for _, c := range []struct {
		name, want string
		getenv     func(string) string
		uid        int
		digests    bool
	}{
		{"from a request", "web server", cgi, 1000, true},
		{"unbound", "digests", none, 1000, false},
		{"root", "root", none, 0, true},
		{"no App", "no such file", none, 1000, true},
	} {
		packageID, manifestDigest, permissionsDigest = "", "", ""
		if c.digests {
			packageID, manifestDigest, permissionsDigest = "synopackageland-runtime-test", "00", "00"
		}
		if err := work(context.Background(), c.getenv, c.uid); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// Config.ExecutorSocket is for Go tests: no CGI variable, query or worker
// environment may move the package CGI or worker off the package's socket,
// or a request could point the runtime at an executor it chose.
func TestNoRequestChoosesTheExecutorSocket(t *testing.T) {
	defer func() { packageID, manifestDigest, permissionsDigest = "", "", "" }()
	packageID, manifestDigest, permissionsDigest = "synopackageland-runtime-test", "00", "00"
	hostile := func(string) string { return "/tmp/attacker/executor.sock" }
	for name, cfg := range map[string]bridge.Config{"CGI": cgiConfig(hostile), "worker": workerConfig()} {
		if cfg.ExecutorSocket != "" {
			t.Errorf("%s: executor socket %q", name, cfg.ExecutorSocket)
		}
	}
	t.Setenv("SYNOPKGLAND_EXECUTOR_SOCKET", "/tmp/attacker/executor.sock")
	if cfg := cgiConfig(os.Getenv); cfg.ExecutorSocket != "" {
		t.Errorf("environment: executor socket %q", cfg.ExecutorSocket)
	}
}

func TestPortalExecutorModeIsBoundByTheBuild(t *testing.T) {
	defer func() { portalMode = "" }()
	for _, mode := range []string{"", "on", "executor"} {
		portalMode = mode
		if got := cgiConfig(func(string) string { return "executor" }).PortalExecutor; got != (mode == "executor") {
			t.Fatal(mode, got)
		}
	}
}
