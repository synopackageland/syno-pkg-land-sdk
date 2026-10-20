//go:build linux

package compose

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageInitCommandExecutesOnlyAfterWritablePreflight(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "storage-init")
	if out, err := exec.Command("go", "build", "-o", binary, "./cmd/storage-init").CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	base := t.TempDir()
	raw, _ := json.Marshal([]WritableDirectory{{Base: base, Path: "app"}})
	// The App sees its literal argv, receives a private umask, and retains its
	// own exit status because the SDK replaces itself with the App process.
	cmd := exec.Command(binary, "--directories", string(raw), "--", "/bin/sh", "-c", `test "$1" = 'literal $value' && touch "$2/app/created" && exit 17`, "app", "literal $value", base)
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 17 {
		t.Fatalf("exec: %s %v", out, err)
	}
	st, err := os.Stat(filepath.Join(base, "app/created"))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("umask: %v %v", st, err)
	}
	// Exercise the shipped binary's new JSON flag and sequencing, not only the
	// in-process API: prepare must run before after_prepare files and main.
	init := StorageInitialization{Directories: []WritableDirectory{{Base: base, Path: "app"}}, Runtime: &RuntimeConfiguration{
		Files:   []RuntimeFile{{Target: filepath.Join(base, "app/literal"), Content: "literal ${NATIVE}"}, {Target: filepath.Join(base, "app/generated"), Source: filepath.Join(base, "app/prepared"), AfterPrepare: true}},
		Prepare: []PrepareCommand{{Argv: []string{"/usr/bin/touch", filepath.Join(base, "app/prepared")}}},
	}}
	config, _ := json.Marshal(init)
	out, err = exec.Command(binary, "--initialization", string(config), "--", "/bin/cat", filepath.Join(base, "app/literal")).CombinedOutput()
	if err != nil || string(out) != "literal ${NATIVE}" {
		t.Fatalf("runtime binary: %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(base, "app/generated")); err != nil {
		t.Fatal("after_prepare did not follow upstream initialization", err)
	}
	init.Runtime.Prepare = []PrepareCommand{{Argv: []string{"/bin/sh", "-c", "echo do-not-log-fixture; exit 4"}}}
	config, _ = json.Marshal(init)
	appMarker := filepath.Join(base, "must-not-run")
	out, err = exec.Command(binary, "--initialization", string(config), "--", "/usr/bin/touch", appMarker).CombinedOutput()
	if err == nil || strings.Contains(string(out), "do-not-log-fixture") {
		t.Fatalf("unsafe prepare failure: %q %v", out, err)
	}
	if _, err := os.Stat(appMarker); !os.IsNotExist(err) {
		t.Fatal("main ran after prepare failure")
	}
	os.Chmod(filepath.Join(base, "app"), 0500)
	defer os.Chmod(filepath.Join(base, "app"), 0700)
	marker := filepath.Join(base, "should-not-run")
	out, err = exec.Command(binary, "--directories", string(raw), "--", "/usr/bin/touch", marker).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "COMPOSE_STORAGE_NOT_WRITABLE") {
		t.Fatalf("unwritable: %s %v", out, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("App ran after preflight failed")
	}
}
