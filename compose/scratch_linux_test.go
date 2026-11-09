//go:build linux

package compose

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Prepare runs as the package account in preinst. Scratch must be created
// explicitly with mode 0700, whatever the umask or a previous mode was.
func TestPrepareCreatesPrivateScratch(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("Prepare refuses root; run as the package UID")
	}
	r := tmpfsRequest(t, "    tmpfs: ['/tmp:mode=1777']\n")
	r.Services["openlist"] = ServicePolicy{Scratch: []string{"/tmp", "/var/cache/app"}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	lock, _ := p.LockJSON()
	os.WriteFile(filepath.Join(project, "compose.lock.json"), lock, 0600)
	// A stale entry and a permissive pre-existing directory from an earlier payload.
	stale := filepath.Join(project, ScratchDirectory, "openlist")
	os.MkdirAll(stale, 0777)
	os.Chmod(stale, 0777)
	os.WriteFile(filepath.Join(stale, "leftover"), []byte("x"), 0600)
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	if err := Prepare(project, "Fixture", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stale, "leftover")); !os.IsNotExist(err) {
		t.Fatal("old scratch content survived")
	}
	for _, target := range []string{"/tmp", "/var/cache/app"} {
		source := scratchSource("Fixture", "openlist", target)
		rel := strings.TrimPrefix(source, "/var/packages/Fixture/target/compose/")
		for _, dir := range []string{ScratchDirectory, filepath.Dir(rel), rel} {
			st, err := os.Lstat(filepath.Join(project, dir))
			if err != nil {
				t.Fatal(err)
			}
			native := st.Sys().(*syscall.Stat_t)
			if !st.IsDir() || st.Mode().Perm() != 0700 || native.Uid != uint32(os.Geteuid()) {
				t.Fatalf("%s: mode %v uid %d", dir, st.Mode(), native.Uid)
			}
		}
	}
}

// The in-container reset is what the App sees: owner = run-as UID, mode 0700,
// empty at every start, and writable on a read-only root filesystem.
func TestResetScratchEmptiesAndSetsPrivateMode(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("scratch reset refuses root; run as the package UID")
	}
	dir := filepath.Join(t.TempDir(), "tmp")
	os.Mkdir(dir, 0777)
	os.Chmod(dir, 0777) // e.g. an inherited ACL that reads as 0777
	os.MkdirAll(filepath.Join(dir, "a/b/c"), 0700)
	os.WriteFile(filepath.Join(dir, "a/b/c/file"), []byte("x"), 0600)
	os.Chmod(filepath.Join(dir, "a/b"), 0500)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0600)
	os.Symlink(outside, filepath.Join(dir, "link"))
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0600)
	for i := range 600 {
		os.WriteFile(filepath.Join(dir, "many-"+string(rune('a'+i%26))+string(rune('a'+i/26))), nil, 0600)
	}
	if err := ResetScratch([]string{dir}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("not emptied: %d entries", len(entries))
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("followed a symlink out of scratch")
	}
	st, _ := os.Stat(dir)
	if st.Mode().Perm() != 0700 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		t.Fatalf("mode %v", st.Mode())
	}
	for _, kind := range []string{"symlink", "missing", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "tmp")
			switch kind {
			case "symlink":
				os.Symlink(t.TempDir(), target)
			case "foreign":
				target = "/usr" // owned by root
			}
			if err := ResetScratch([]string{target}); err == nil || !strings.Contains(err.Error(), "COMPOSE_SCRATCH_NOT_READY") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}

// The shipped initializer empties scratch before it runs the App argv.
func TestStorageInitCommandResetsScratchBeforeApp(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("run as the package UID")
	}
	binary := filepath.Join(t.TempDir(), "storage-init")
	if out, err := exec.Command("go", "build", "-o", binary, "./cmd/storage-init").CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	scratch := filepath.Join(t.TempDir(), "tmp")
	os.Mkdir(scratch, 0755)
	os.WriteFile(filepath.Join(scratch, "previous-run"), []byte("x"), 0600)
	raw, _ := json.Marshal(StorageInitialization{Scratch: []string{scratch}})
	out, err := exec.Command(binary, "--initialization", string(raw), "--", "/bin/sh", "-c", `test -z "$(ls -A "$1")" && stat -c '%a %u' "$1" && touch "$1/created"`, "app", scratch).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if got := strings.TrimSpace(string(out)); got != "700 "+itoa(os.Geteuid()) {
		t.Fatalf("App saw %q", got)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Shared Folder roots belong to DSM or an administrator. The fixture root is
// prepared by the test runner as root; the test itself runs as the package UID.
func TestInitializeStorageOnForeignSharedFolder(t *testing.T) {
	fixtures := os.Getenv("SYNOPKGLAND_TEST_FOREIGN_SHARES")
	if fixtures == "" || os.Geteuid() == 0 {
		t.Skip("needs root-prepared Shared Folder fixtures and a nonroot test UID")
	}
	for _, name := range []string{"share-acl-777", "share-group", "share-precreated"} {
		t.Run(name, func(t *testing.T) {
			base := filepath.Join(fixtures, name)
			st, _ := os.Stat(base)
			if st.Sys().(*syscall.Stat_t).Uid == uint32(os.Geteuid()) {
				t.Fatal("fixture root must be owned by another UID")
			}
			before := st.Mode()
			d := WritableDirectory{Base: base, Path: "app/home", Share: true}
			if err := InitializeStorage([]WritableDirectory{d}); err != nil {
				t.Fatal(err)
			}
			// Again on the next start, with existing data.
			os.WriteFile(filepath.Join(base, "app/home/data"), []byte("kept"), 0600)
			if err := InitializeStorage([]WritableDirectory{d}); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(base, "app/home/data")); string(b) != "kept" {
				t.Fatal("data lost")
			}
			after, _ := os.Stat(base)
			if after.Mode() != before || after.Sys().(*syscall.Stat_t).Uid != st.Sys().(*syscall.Stat_t).Uid {
				t.Fatal("share root changed")
			}
			created, _ := os.Stat(filepath.Join(base, "app/home"))
			if name != "share-precreated" && created.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
				t.Fatal("subdirectory not created as the package UID")
			}
			// Package-storage rules still apply when the base is not a Share.
			d.Share = false
			if err := InitializeStorage([]WritableDirectory{d}); err == nil {
				t.Fatal("a foreign writable base passed as package storage")
			}
		})
	}
	t.Run("share-readonly", func(t *testing.T) {
		err := InitializeStorage([]WritableDirectory{{Base: filepath.Join(fixtures, "share-readonly"), Path: "app", Share: true}})
		if err == nil || !strings.Contains(err.Error(), "COMPOSE_STORAGE_NOT_WRITABLE") {
			t.Fatal(err)
		}
	})
	t.Run("share-symlink", func(t *testing.T) {
		base := filepath.Join(fixtures, "share-acl-777")
		os.Symlink(t.TempDir(), filepath.Join(base, "link"))
		err := InitializeStorage([]WritableDirectory{{Base: base, Path: "link", Share: true}})
		if err == nil {
			t.Fatal("symlink followed")
		}
	})
}
