package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func write(t *testing.T, p string, m os.FileMode) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0700)
	if err := os.WriteFile(p, []byte("x"), m); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, m)
}

func TestFixPermissionsTightensOnlyExistingTablePaths(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".ssh/id_ed25519"), 0666)
	write(t, filepath.Join(home, ".ssh/id_rsa_work"), 0644)
	write(t, filepath.Join(home, ".ssh/id_ed25519.pub"), 0644)
	write(t, filepath.Join(home, ".ssh/config"), 0664)
	write(t, filepath.Join(home, ".ssh/known_hosts"), 0644)
	write(t, filepath.Join(home, ".kube/config"), 0644)
	write(t, filepath.Join(home, ".aws/credentials"), 0400)
	os.Chmod(filepath.Join(home, ".ssh"), 0777)
	os.Mkdir(filepath.Join(home, ".gnupg"), 0755)
	os.Chmod(filepath.Join(home, ".gnupg"), 0755)
	var logs []string
	changed := FixPermissions(home, func(s string) { logs = append(logs, s) })
	want := map[string]os.FileMode{".ssh": 0700, ".ssh/id_ed25519": 0600, ".ssh/id_rsa_work": 0600, ".ssh/config": 0600, ".gnupg": 0700, ".kube/config": 0600,
		".ssh/id_ed25519.pub": 0644, ".ssh/known_hosts": 0644, ".aws/credentials": 0400, ".kube": 0700}
	for rel, m := range want {
		if got := mode(t, filepath.Join(home, rel)); got != m {
			t.Errorf("%s: %o, want %o", rel, got, m)
		}
	}
	if len(changed) != 6 || len(logs) != 0 {
		t.Fatalf("changed %v logs %v", changed, logs)
	}
	for _, absent := range []string{".netrc", ".pgpass", ".docker", ".docker/config.json"} {
		if _, err := os.Lstat(filepath.Join(home, absent)); !os.IsNotExist(err) {
			t.Fatalf("%s was created", absent)
		}
	}
	if again := FixPermissions(home, func(string) {}); len(again) != 0 {
		t.Fatalf("second run changed %v", again)
	}
}

func TestFixPermissionsNeverFollowsSymlinks(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	write(t, filepath.Join(outside, "secret"), 0644)
	os.Mkdir(filepath.Join(outside, "dir"), 0755)
	os.Chmod(filepath.Join(outside, "dir"), 0755)
	write(t, filepath.Join(outside, "kube/config"), 0644)
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(home, ".netrc"))
	os.Symlink(filepath.Join(outside, "dir"), filepath.Join(home, ".ssh"))
	os.Symlink(filepath.Join(outside, "kube"), filepath.Join(home, ".kube"))
	var logs []string
	changed := FixPermissions(home, func(s string) { logs = append(logs, s) })
	if len(changed) != 0 || mode(t, filepath.Join(outside, "secret")) != 0644 || mode(t, filepath.Join(outside, "dir")) != 0755 || mode(t, filepath.Join(outside, "kube/config")) != 0644 {
		t.Fatalf("followed a symlink: %v", changed)
	}
	if len(logs) != 3 || !strings.Contains(strings.Join(logs, "\n"), "symlinks are not followed") {
		t.Fatalf("logs: %v", logs)
	}
}

func TestFixPermissionsLeavesForeignOwnedFilesWithOneLine(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".pgpass"), 0644)
	var logs []string
	// Files are owned by the test UID; present them to a different UID.
	other := uint32(os.Geteuid() + 1)
	if changed := fixPermissions(home, other, func(s string) { logs = append(logs, s) }); len(changed) != 0 {
		t.Fatal(changed)
	}
	if mode(t, filepath.Join(home, ".pgpass")) != 0644 || len(logs) != 1 || !strings.Contains(logs[0], fmt.Sprintf("owned by UID %d, not the package UID %d", os.Geteuid(), other)) {
		t.Fatalf("logs %v", logs)
	}
}

func fakeAccounts(t *testing.T) {
	dir := t.TempDir()
	oldP, oldG, oldS := etcPasswd, etcGroup, sudoPaths
	t.Cleanup(func() { etcPasswd, etcGroup, sudoPaths = oldP, oldG, oldS })
	etcPasswd, etcGroup = filepath.Join(dir, "passwd"), filepath.Join(dir, "group")
	os.WriteFile(etcPasswd, []byte("root:x:0:0:root:/root:/bin/bash\n"), 0600)
	os.WriteFile(etcGroup, []byte("root:x:0:\n"), 0600)
	sudoPaths = []string{filepath.Join(dir, "sudo")}
}

func TestConsoleInitKeepsThePackageUIDAndOwnsPreparedDirectories(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as the nonroot package UID")
	}
	fakeAccounts(t)
	// A setuid stand-in for sudo: the owner may set the bit on its own file.
	os.WriteFile(sudoPaths[0], []byte("#!/bin/sh\nexit 0\n"), 0755)
	if err := os.Chmod(sudoPaths[0], 0755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	c := ConsoleConfig{Console: Console{Home: filepath.Join(base, "home"), User: "fixture", Tmp: filepath.Join(base, "tmp")}, Sudo: true}
	env, err := InitializeConsole(context.Background(), c, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{c.Home, c.Tmp} {
		var st syscall.Stat_t
		if err := syscall.Lstat(d, &st); err != nil || int(st.Uid) != os.Geteuid() || int(st.Gid) != os.Getegid() {
			t.Fatalf("%s owner %d:%d, want the package UID %d", d, st.Uid, st.Gid, os.Geteuid())
		}
	}
	if mode(t, c.Tmp) != 0700 || env["TMPDIR"] != c.Tmp || env["HOME"] != c.Home || env["USER"] != "fixture" || !slices.Contains(strings.Split(env["PATH"], ":"), ConsoleBin) {
		t.Fatalf("env %v", env)
	}
	passwd, _ := os.ReadFile(etcPasswd)
	want := fmt.Sprintf("fixture:*:%d:%d:fixture:%s:", os.Geteuid(), os.Getegid(), c.Home)
	if !strings.Contains(string(passwd), want) {
		t.Fatalf("passwd %s", passwd)
	}
	if os.Geteuid() == 0 || os.Getuid() != os.Geteuid() {
		t.Fatal("init changed the process UID")
	}
	// HOME may be a selected folder owned by an administrator: log, do not fail.
	var logs []string
	if err := privateDirectory("/", false, func(s string) { logs = append(logs, s) }); err != nil || len(logs) != 1 || !strings.Contains(logs[0], "used as is") {
		t.Fatalf("foreign HOME: %v %v", err, logs)
	}
	if err := privateDirectory("/", true, func(string) {}); err == nil {
		t.Fatal("a foreign TMPDIR was accepted")
	}
	// A second start must not duplicate the entry.
	if _, err := InitializeConsole(context.Background(), c, func(string) {}); err != nil {
		t.Fatal(err)
	}
	passwd, _ = os.ReadFile(etcPasswd)
	if strings.Count(string(passwd), want) != 1 {
		t.Fatalf("duplicate entry: %s", passwd)
	}
}

func TestConsoleInitFailsClearlyWithoutSudo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as the nonroot package UID")
	}
	fakeAccounts(t)
	base := t.TempDir()
	c := ConsoleConfig{Console: Console{Home: filepath.Join(base, "home"), User: "fixture"}, Sudo: true}
	if _, err := InitializeConsole(context.Background(), c, func(string) {}); err == nil || !strings.Contains(err.Error(), "COMPOSE_CONSOLE_SUDO_UNAVAILABLE") {
		t.Fatalf("missing sudo: %v", err)
	}
	fakeAccounts(t)
	os.WriteFile(sudoPaths[0], []byte("#!/bin/sh\nexit 0\n"), 0755)
	os.Chmod(sudoPaths[0], 0755|os.ModeSetuid)
	os.Chmod(etcPasswd, 0400)
	if _, err := InitializeConsole(context.Background(), c, func(string) {}); err == nil || !strings.Contains(err.Error(), "append /etc/passwd") {
		t.Fatalf("read-only passwd: %v", err)
	}
}

func TestConsoleInitWithoutSudoUsesNSSWrapperOrLogsOneLine(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as the nonroot package UID")
	}
	fakeAccounts(t)
	base := t.TempDir()
	old := NSSWrapperLibraries
	t.Cleanup(func() { NSSWrapperLibraries = old })
	NSSWrapperLibraries = []string{filepath.Join(base, "missing.so")}
	c := ConsoleConfig{Console: Console{Home: filepath.Join(base, "home"), User: "fixture", Tmp: filepath.Join(base, "tmp")}}
	var logs []string
	env, err := InitializeConsole(context.Background(), c, func(s string) { logs = append(logs, s) })
	if err != nil || len(logs) != 1 || !strings.Contains(logs[0], "libnss_wrapper") || env["LD_PRELOAD"] != "" {
		t.Fatalf("env %v logs %v err %v", env, logs, err)
	}
	lib := filepath.Join(base, "libnss_wrapper.so")
	os.WriteFile(lib, nil, 0644)
	NSSWrapperLibraries = []string{lib}
	env, err = InitializeConsole(context.Background(), c, func(string) {})
	if err != nil || !strings.HasPrefix(env["LD_PRELOAD"], lib) || !strings.HasPrefix(env["NSS_WRAPPER_PASSWD"], c.Tmp+"/") {
		t.Fatalf("env %v %v", env, err)
	}
	p, _ := os.ReadFile(env["NSS_WRAPPER_PASSWD"])
	if !strings.Contains(string(p), fmt.Sprintf("fixture:*:%d:", os.Geteuid())) || !strings.Contains(string(p), "root:x:0:0") {
		t.Fatalf("wrapper passwd %s", p)
	}
	if real, _ := os.ReadFile(etcPasswd); strings.Contains(string(real), "fixture") {
		t.Fatal("without sudo the real passwd must not change")
	}
}

func TestFixPermissionsCapsFilesAt0600AndDirectoriesAt0700(t *testing.T) {
	home := t.TempDir()
	// What ssh-keygen leaves in a folder whose ACL widens new files.
	write(t, filepath.Join(home, ".ssh/id_ed25519"), 0777)
	write(t, filepath.Join(home, ".ssh/id_ed25519.pub"), 0777)
	write(t, filepath.Join(home, ".ssh/known_hosts"), 0777)
	write(t, filepath.Join(home, ".ssh/config"), 0700)
	write(t, filepath.Join(home, ".netrc"), 0500)
	write(t, filepath.Join(home, ".pgpass"), 0400)
	write(t, filepath.Join(home, ".docker/config.json"), 0600)
	os.Chmod(filepath.Join(home, ".ssh"), 0777)
	os.Mkdir(filepath.Join(home, ".gnupg"), 0700)
	os.Chmod(filepath.Join(home, ".gnupg"), 0700|os.ModeSetgid)
	var logs []string
	changed := FixPermissions(home, func(s string) { logs = append(logs, s) })
	want := map[string]os.FileMode{".ssh": 0700, ".ssh/id_ed25519": 0600, ".ssh/config": 0600, ".netrc": 0400, ".pgpass": 0400, ".docker/config.json": 0600, ".gnupg": 0700,
		// Not in the table: never touched.
		".ssh/id_ed25519.pub": 0777, ".ssh/known_hosts": 0777}
	for rel, m := range want {
		if got := mode(t, filepath.Join(home, rel)); got != m {
			t.Errorf("%s: %o, want %o", rel, got, m)
		}
	}
	if st, _ := os.Lstat(filepath.Join(home, ".gnupg")); st.Mode()&os.ModeSetgid != 0 {
		t.Error(".gnupg kept setgid")
	}
	slices.Sort(changed)
	wantChanged := []string{filepath.Join(home, ".gnupg"), filepath.Join(home, ".netrc"), filepath.Join(home, ".ssh"), filepath.Join(home, ".ssh/config"), filepath.Join(home, ".ssh/id_ed25519")}
	if !slices.Equal(changed, wantChanged) || len(logs) != 0 {
		t.Fatalf("changed %v logs %v", changed, logs)
	}
	if again := FixPermissions(home, func(string) {}); len(again) != 0 {
		t.Fatalf("second run changed %v", again)
	}
}

func TestFixPermissionsKeepsTheMountedFolderItself(t *testing.T) {
	home := t.TempDir()
	os.Chmod(home, 0777)
	write(t, filepath.Join(home, ".ssh/id_rsa"), 0777)
	FixPermissions(home, func(string) {})
	if mode(t, home) != 0777 || mode(t, filepath.Join(home, ".ssh/id_rsa")) != 0600 {
		t.Fatalf("home %o key %o", mode(t, home), mode(t, filepath.Join(home, ".ssh/id_rsa")))
	}
}
