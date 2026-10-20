//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// openPart opens a fresh part in dest with content, as the move child does.
func openPart(t *testing.T, dest, content string) *os.File {
	t.Helper()
	p := filepath.Join(dest, ".synopkgland-job-part.part")
	os.Remove(p)
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPublishFailKeepsExistingFile(t *testing.T) {
	dest, dir := openDest(t)
	os.WriteFile(filepath.Join(dest, "a.txt"), []byte("old"), 0600)
	if _, err := publishPart(openPart(t, dest, "new"), dir, "a.txt", ""); publishCode(err) != "destination-exists" {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dest, "a.txt")) != "old" {
		t.Fatal("fail policy replaced the file")
	}
}

func TestPublishOverwriteReplacesAtomically(t *testing.T) {
	dest, dir := openDest(t)
	target := filepath.Join(dest, "a.txt")
	os.WriteFile(target, []byte("old"), 0600)
	before, _ := os.Open(target)
	defer before.Close()
	name, err := publishPart(openPart(t, dest, "new"), dir, "a.txt", conflictOverwrite)
	if err != nil || name != "a.txt" {
		t.Fatal(name, err)
	}
	if read(t, target) != "new" {
		t.Fatal("not replaced")
	}
	// The replaced inode is untouched: a reader holding it sees the old bytes.
	b := make([]byte, 3)
	if _, err := before.ReadAt(b, 0); err != nil || string(b) != "old" {
		t.Fatal("old file was rewritten in place", string(b), err)
	}
	if leftovers(t, dest) != 1 {
		t.Fatal("temporary name left behind")
	}
	if name, err = publishPart(openPart(t, dest, "first"), dir, "b.txt", conflictOverwrite); err != nil || read(t, filepath.Join(dest, "b.txt")) != "first" {
		t.Fatal("overwrite of a missing file", name, err)
	}
}

func TestPublishOverwriteRefusesNonRegularAndUnwritableTargets(t *testing.T) {
	dest, dir := openDest(t)
	os.Mkdir(filepath.Join(dest, "folder"), 0700)
	os.Symlink("/etc/passwd", filepath.Join(dest, "link"))
	syscall.Mkfifo(filepath.Join(dest, "fifo"), 0600)
	for _, name := range []string{"folder", "link", "fifo"} {
		if _, err := publishPart(openPart(t, dest, "new"), dir, name, conflictOverwrite); publishCode(err) != "destination-exists" {
			t.Fatal(name, err)
		}
	}
	if target, _ := os.Readlink(filepath.Join(dest, "link")); target != "/etc/passwd" {
		t.Fatal("symlink replaced")
	}
	if os.Geteuid() != 0 {
		os.WriteFile(filepath.Join(dest, "ro"), []byte("old"), 0400)
		if _, err := publishPart(openPart(t, dest, "new"), dir, "ro", conflictOverwrite); publishCode(err) != "PERMISSION_DENIED" {
			t.Fatal("replaced a file the user cannot write", err)
		}
		if read(t, filepath.Join(dest, "ro")) != "old" {
			t.Fatal("read-only file changed")
		}
	}
	if leftovers(t, dest) != 1 {
		t.Fatal("temporary name left behind")
	}
}

func TestPublishRenamePicksNextFreeName(t *testing.T) {
	dest, dir := openDest(t)
	os.WriteFile(filepath.Join(dest, "a.txt"), []byte("0"), 0600)
	os.WriteFile(filepath.Join(dest, "a (1).txt"), []byte("1"), 0600)
	name, err := publishPart(openPart(t, dest, "new"), dir, "a.txt", conflictRename)
	if err != nil || name != "a (2).txt" || read(t, filepath.Join(dest, "a (2).txt")) != "new" {
		t.Fatal(name, err)
	}
	if read(t, filepath.Join(dest, "a.txt")) != "0" {
		t.Fatal("rename touched the original")
	}
	name, err = publishPart(openPart(t, dest, "free"), dir, "free.txt", conflictRename)
	if err != nil || name != "free.txt" {
		t.Fatal("free name must be used as is", name, err)
	}
}

func TestPublishRenameStopsAfterTheLastCandidate(t *testing.T) {
	dest, dir := openDest(t)
	os.WriteFile(filepath.Join(dest, "a"), nil, 0600)
	for n := 1; n <= maxRenameAttempts; n++ {
		os.WriteFile(filepath.Join(dest, renameCandidate("a", n)), nil, 0600)
	}
	if _, err := publishPart(openPart(t, dest, "new"), dir, "a", conflictRename); publishCode(err) != "destination-exists" {
		t.Fatal(err)
	}
}

func TestPublishedPartFindsRenamedName(t *testing.T) {
	dest, dir := openDest(t)
	os.WriteFile(filepath.Join(dest, "a.txt"), []byte("old"), 0600)
	part := openPart(t, dest, "new")
	name, err := publishPart(part, dir, "a.txt", conflictRename)
	if err != nil {
		t.Fatal(err)
	}
	spec := moveSpec{Subject: Subject{UID: os.Getuid()}, Name: "a.txt", Conflict: conflictRename, Size: 3, Part: partMetadata{Part: mustIdentity(t, part)}}
	if got, ok := publishedPart(dir, spec); !ok || got != name {
		t.Fatal(got, ok)
	}
	spec.Conflict = ""
	if _, ok := publishedPart(dir, spec); ok {
		t.Fatal("fail policy matched a renamed name")
	}
}

// leftovers counts the entries in dest whose name is a publish temporary.
func leftovers(t *testing.T, dest string) int {
	t.Helper()
	entries, _ := os.ReadDir(dest)
	n := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".synopkgland-") {
			n++
		}
	}
	return n
}
