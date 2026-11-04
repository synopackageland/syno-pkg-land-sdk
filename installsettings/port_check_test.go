package installsettings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serviceToolFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "service tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPortCheckToolFailuresAreNotConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason, stderr string
		code                       int
	}{
		{"missing", "", "exec", "", -1},
		{"failed", "echo 'registry unavailable' >&2; exit 23", "exec", "registry unavailable", 23},
		{"malformed", "echo 'unexpected service response'", "response", "", 0},
		{"wrongPort", "echo 'IsConflict: false Port: 29001 Protocol: tcp ServiceName: (null)'", "response", "", 0},
		{"wrongProtocol", "echo 'IsConflict: false Port: 29000 Protocol: udp ServiceName: (null)'", "response", "", 0},
		{"oversized", "while [ ${i:-0} -lt 20000 ]; do printf x; i=$((${i:-0}+1)); done", "response", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := filepath.Join(t.TempDir(), "missing")
			if tc.body != "" {
				tool = serviceToolFixture(t, tc.body)
			}
			err := checkPort(context.Background(), tool, "ExampleApp", "gateway_http", Port{Protocol: "tcp"}, 29000)
			var failure *PortCheckError
			if !errors.As(err, &failure) || !errors.Is(err, ErrPortCheck) || errors.Is(err, ErrPort) {
				t.Fatalf("tool failure classified as a port conflict: %v", err)
			}
			if failure.ExitCode != tc.code || failure.Reason != tc.reason || failure.Elapsed <= 0 || failure.Stderr != tc.stderr {
				t.Fatalf("lost tool diagnostics: %+v", failure)
			}
			for _, want := range []string{"INSTALL_PORT_CHECK_FAILED", "retry", "exit_code=", "elapsed=", tc.stderr} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("missing %q: %v", want, err)
				}
			}
		})
	}
}

func TestPortCheckKeepsOwnServiceExceptionAndRealConflicts(t *testing.T) {
	for _, tc := range []struct {
		response string
		conflict bool
	}{
		{"IsConflict: false\tPort: 29000\tProtocol: tcp\tServiceName: (null)", false},
		{"IsConflict: true Port: 29000 Protocol: tcp ServiceName: synopkgland.Example-App.gateway-http", false},
		{"IsConflict: true Port: 29000 Protocol: tcp ServiceName: synopkgland.OtherApp.gateway-http", true},
		{"IsConflict: true Port: 29000 Protocol: tcp ServiceName: synopkgland.Example-App.other-port", true},
	} {
		tool := serviceToolFixture(t, "printf '%s\\n' '"+tc.response+"'")
		err := checkPort(context.Background(), tool, "Example_App", "gateway_http", Port{Protocol: "tcp"}, 29000)
		if tc.conflict {
			if !errors.Is(err, ErrPort) || errors.Is(err, ErrPortCheck) {
				t.Fatalf("real conflict misclassified: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestPortCheckIgnoresPathAndPreservesCancellation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "path-tool-ran")
	shim := serviceToolFixture(t, ": > '"+marker+"'")
	if err := os.Rename(shim, filepath.Join(filepath.Dir(shim), "servicetool")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(shim))
	_ = CheckPort(context.Background(), "ExampleApp", "gateway_http", Port{Protocol: "tcp"}, 29000)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("used untrusted PATH servicetool")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := checkPort(ctx, serviceToolFixture(t, "exit 0"), "ExampleApp", "gateway_http", Port{Protocol: "tcp"}, 29000)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrPortCheck) {
		t.Fatalf("caller cancellation lost: %v", err)
	}
}

func TestPreparePortsPreservesToolFailureAndKey(t *testing.T) {
	root, state := portFixture(t)
	failure := &PortCheckError{Reason: "exec", ExitCode: 12, Stderr: "registry not ready", Elapsed: time.Second}
	check := func(context.Context, string, string, Port, int) error { return failure }
	err := preparePorts(root, "ExampleApp", state, check)
	if !errors.Is(err, failure) || !errors.Is(err, ErrPortCheck) || errors.Is(err, ErrPort) || !strings.Contains(err.Error(), "(gateway_http)") {
		t.Fatalf("lost tool failure at installer boundary: %v", err)
	}
	raw, readErr := os.ReadFile(filepath.Join(root, "sso/gateway.json"))
	if readErr != nil || string(raw) != `{"listen":{"http":28000},"timer":28000}` {
		t.Fatalf("failed check rewrote payload: %v %s", readErr, raw)
	}
}

func TestPortCheckStderrIsBoundedAndSingleLine(t *testing.T) {
	tool := serviceToolFixture(t, "printf 'registry\\nnot ready\\033' >&2; while [ ${i:-0} -lt 1000 ]; do printf x >&2; i=$((${i:-0}+1)); done; exit 9")
	err := checkPort(context.Background(), tool, "ExampleApp", "gateway_http", Port{Protocol: "tcp"}, 29000)
	var failure *PortCheckError
	if !errors.As(err, &failure) || len(failure.Stderr) > 515 || strings.ContainsAny(err.Error(), "\n\r\x1b") {
		t.Fatalf("unbounded diagnostic: %v", err)
	}
}
