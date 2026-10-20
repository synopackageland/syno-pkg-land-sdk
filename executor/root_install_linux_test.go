//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Unlike execution-only fixtures, installation validates every runtime ancestor.
func rootInstallLauncher(t *testing.T) rootPrograms {
	t.Helper()
	r := rootLauncher(t)
	dir, err := os.MkdirTemp("/root", "sdk-root-install-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	r.Dir = dir
	if err := os.Mkdir(filepath.Join(dir, "root"), 0700); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInstallRootProgramsSharesOnlyPinnedBinaryBytes(t *testing.T) {
	r := rootInstallLauncher(t)
	source := t.TempDir()
	write := func(name, text string) RootEntry {
		t.Helper()
		path := filepath.Join(source, name)
		if err := os.WriteFile(path, []byte(text), 0700); err != nil {
			t.Fatal(err)
		}
		digest, err := digestFile(context.Background(), path, maxExecutorBytes)
		if err != nil {
			t.Fatal(err)
		}
		return RootEntry{Digest: digest, Kind: "binary"}
	}
	a := write("cli", "same binary")
	write("lifecycle", "same binary")
	b := write("other", "different binary")
	write("script.sh", "same binary")
	script := a
	script.Kind = "script"
	entries := map[string]RootEntry{"cli": a, "lifecycle": a, "other": b, "script": script}
	install := func() {
		t.Helper()
		if err := installRootPrograms(source, r, entries); err != nil {
			t.Fatal(err)
		}
	}
	install()
	cli, _ := os.Stat(r.path("cli", "binary"))
	life, _ := os.Stat(r.path("lifecycle", "binary"))
	if !os.SameFile(cli, life) {
		t.Fatal("identical pinned binaries still occupy two inodes")
	}
	for name, entry := range entries {
		f, err := openPinned(r.path(name, entry.Kind), entry.Kind, entry.Digest)
		if err != nil {
			t.Fatal(name, err)
		}
		f.Close()
	}
	for _, path := range []string{filepath.Join(source, "cli"), r.path("other", "binary"), r.path("script", "script")} {
		info, _ := os.Stat(path)
		if os.SameFile(cli, info) {
			t.Fatal("shared untrusted source, different bytes, or script", path)
		}
	}
	// Publish replaces inodes, never truncates a shared or already-open program.
	held, err := openPinned(r.path("cli", "binary"), "binary", a.Digest)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	entries["lifecycle"] = write("lifecycle", "new lifecycle")
	install()
	contents, _ := os.ReadFile(r.path("cli", "binary"))
	if string(contents) != "same binary" {
		t.Fatal("upgrade changed cli through old hard link")
	}
	still, _ := held.Stat()
	if !os.SameFile(cli, still) {
		t.Fatal("open old program lost its inode")
	}
	install() // repeated start is safe too
}

func TestInstallRootProgramsRejectsUnpinnedSourcesBeforePublishing(t *testing.T) {
	for _, bad := range []string{"digest", "missing", "symlink", "directory", "invalid entry"} {
		t.Run(bad, func(t *testing.T) {
			r := rootInstallLauncher(t)
			source := t.TempDir()
			entry := pin(t, r, "cli", "binary", []byte("approved"), 0700)
			os.WriteFile(filepath.Join(source, "cli"), []byte("approved"), 0700)
			os.WriteFile(filepath.Join(source, "lifecycle"), []byte("approved"), 0700)
			entries := map[string]RootEntry{"cli": entry, "lifecycle": entry}
			switch bad {
			case "digest":
				os.WriteFile(filepath.Join(source, "lifecycle"), []byte("tampered"), 0700)
			case "missing":
				os.Remove(filepath.Join(source, "lifecycle"))
			case "symlink":
				os.Remove(filepath.Join(source, "lifecycle"))
				os.Symlink(filepath.Join(source, "cli"), filepath.Join(source, "lifecycle"))
			case "directory":
				os.Remove(filepath.Join(source, "lifecycle"))
				os.Mkdir(filepath.Join(source, "lifecycle"), 0700)
			case "invalid entry":
				entries["../escape"] = entry
			}
			before, _ := os.Stat(r.path("cli", "binary"))
			if err := installRootPrograms(source, r, entries); err == nil {
				t.Fatal("unapproved input accepted")
			}
			after, _ := os.Stat(r.path("cli", "binary"))
			if !os.SameFile(before, after) {
				t.Fatal("published before validating the entire inventory")
			}
			if _, err := os.Stat(r.path("lifecycle", "binary")); !os.IsNotExist(err) {
				t.Fatal("rejected lifecycle installed", err)
			}
			paths, _ := filepath.Glob(filepath.Join(r.Dir, ".root-*"))
			if len(paths) != 0 {
				t.Fatal("temporary inventory leaked", paths)
			}
		})
	}
}

func TestInstallRootProgramsExecutesAliasesWithTheirOwnNameAndMode(t *testing.T) {
	r := rootInstallLauncher(t)
	source := t.TempDir()
	binary, err := os.ReadFile("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cli", "lifecycle"} {
		if err = os.WriteFile(filepath.Join(source, name), binary, 0700); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := digestFile(context.Background(), filepath.Join(source, "cli"), maxExecutorBytes)
	if err != nil {
		t.Fatal(err)
	}
	entry := RootEntry{Kind: "binary", Digest: digest}
	if err = installRootPrograms(source, r, map[string]RootEntry{"cli": entry, "lifecycle": entry}); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct{ name, mode string }{{"cli", "run"}, {"lifecycle", "stop"}, {"lifecycle", "purge"}} {
		code, out, err := runLinux(t, r, rootCall{Name: call.name, Entry: entry, Mode: call.mode, Args: []string{"-c", `printf '%s:%s' "$0" "$SYNOPKGLAND_ROOT_MODE"`}, Env: []string{"SYNOPKGLAND_ROOT_MODE=" + call.mode}}, 10*time.Second)
		if err != nil || code != 0 || out != call.name+":"+call.mode {
			t.Fatal(code, out, err)
		}
	}
	// Even a root-side write through a shared inode cannot bypass either pin.
	if err := os.WriteFile(r.path("cli", "binary"), []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cli", "lifecycle"} {
		f, err := openPinned(r.path(name, "binary"), "binary", digest)
		if err == nil {
			f.Close()
			t.Fatal("changed shared bytes accepted", name)
		}
	}
}
