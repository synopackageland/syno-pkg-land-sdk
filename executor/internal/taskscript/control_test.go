package taskscript

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the canonical shell with temporary package/runtime paths only.
// Both the CLI and lifecycle helper render this same embedded source.
func TestControlRefusalLogsPackageAndRemovesRejectedCopies(t *testing.T) {
	for _, reason := range []string{"executor", "policy", "missing executor", "missing policy", "missing INFO", "missing anchor", "duplicate anchor", "invalid anchor", "stop policy"} {
		t.Run(reason, func(t *testing.T) {
			root := t.TempDir()
			pkg, state := filepath.Join(root, "package"), filepath.Join(root, "state")
			source, runtimeDir := filepath.Join(pkg, "target/executor"), filepath.Join(state, "runtime")
			for _, dir := range []string{source, runtimeDir, filepath.Join(pkg, "var/executor")} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, text string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(text), 0700); err != nil {
					t.Fatal(err)
				}
			}
			binary := "#!/bin/sh\necho executed >> \"$CALLS\"\n"
			policy := "{}\n"
			anchor := fmt.Sprintf("synopkgland_executor_sha256=\"%x\"\n", sha256.Sum256([]byte(binary)))
			wantLog := "digest"
			for _, dir := range []string{source, runtimeDir} {
				write(filepath.Join(dir, "executor"), binary)
				write(filepath.Join(dir, "policy.json"), policy)
			}
			// Anchor rejection happens before copying; even stale temporary files
			// from an earlier refusal must be removed, without touching live data.
			for _, file := range []string{".executor", ".policy.json", "keep"} {
				write(filepath.Join(runtimeDir, file), "retained")
			}
			action := "start"
			switch reason {
			case "executor":
				write(filepath.Join(source, "executor"), binary+"# tampered\n")
			case "policy":
				write(filepath.Join(source, "policy.json"), "tampered")
			case "missing executor", "missing policy":
				file := "executor"
				if reason == "missing policy" {
					file = "policy.json"
				}
				if err := os.Remove(filepath.Join(source, file)); err != nil {
					t.Fatal(err)
				}
			case "missing INFO":
				wantLog = "anchor"
			case "missing anchor":
				anchor, wantLog = "", "anchor"
			case "duplicate anchor":
				anchor, wantLog = anchor+anchor, "anchor"
			case "invalid anchor":
				anchor, wantLog = "synopkgland_executor_sha256=\"invalid\"\n", "anchor"
			case "stop policy":
				action = "stop"
				write(filepath.Join(runtimeDir, "policy.json"), "tampered")
			}
			write(filepath.Join(pkg, "INFO"), anchor)
			if reason == "missing INFO" {
				if err := os.Remove(filepath.Join(pkg, "INFO")); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(pkg, "var/executor/control"), action)
			tasks, err := Generate("TestApp", fmt.Sprintf("%x", sha256.Sum256([]byte(policy))))
			if err != nil {
				t.Fatal(err)
			}
			script := strings.NewReplacer(
				"/var/packages/$p", pkg,
				"/usr/local/etc/synopkgland/$p", state,
				"-uid 0", fmt.Sprintf("-uid %d", os.Getuid()),
			).Replace(tasks[1].Script)
			if runtime.GOOS == "darwin" {
				script = strings.ReplaceAll(script, "sha256sum -c -", "shasum -a 256 -c -")
			}
			script = strings.Replace(script, "export PATH=/usr/bin:/bin\n", "export PATH=/usr/bin:/bin\nlogger(){ printf '%s\\n' \"$*\" >> \"$LOG\"; }\n", 1)
			path := filepath.Join(root, "control.sh")
			write(path, script)
			command := exec.Command("sh", path)
			command.Env = append(os.Environ(), "LOG="+filepath.Join(root, "log"), "CALLS="+filepath.Join(root, "calls"))
			if out, err := command.CombinedOutput(); err == nil {
				t.Fatalf("tampered input ran successfully: %s", out)
			}
			log, err := os.ReadFile(filepath.Join(root, "log"))
			if err != nil || string(log) != "-t synopkgland TestApp: "+wantLog+"\n" {
				t.Errorf("package log = %q, error %v", log, err)
			}
			for _, file := range []string{".executor", ".policy.json"} {
				if _, err := os.Lstat(filepath.Join(runtimeDir, file)); !os.IsNotExist(err) {
					t.Errorf("rejected %s survived: %v", file, err)
				}
			}
			for file, want := range map[string]string{"executor": binary, "policy.json": policy, "keep": "retained"} {
				if reason == "stop policy" && file == "policy.json" {
					want = "tampered"
				}
				if got, err := os.ReadFile(filepath.Join(runtimeDir, file)); err != nil || string(got) != want {
					t.Errorf("live %s changed: %q, %v", file, got, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "calls")); !os.IsNotExist(err) {
				t.Errorf("executor ran after rejection: %v", err)
			}
		})
	}
}
