//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func openDest(t *testing.T) (string, *os.File) {
	t.Helper()
	dest := t.TempDir()
	dir, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dir.Close() })
	return dest, dir
}

func TestOpenFoldersCreatesMissingAndReusesExisting(t *testing.T) {
	dest, dir := openDest(t)
	os.Mkdir(filepath.Join(dest, "Album"), 0755)
	leaf, err := openFolders(dir, []string{"Album", "CD1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer leaf.Close()
	info, err := os.Stat(filepath.Join(dest, "Album", "CD1"))
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal(info, err)
	}
	if info, _ := os.Stat(filepath.Join(dest, "Album")); info.Mode().Perm() != 0755 {
		t.Fatal("existing folder changed", info.Mode())
	}
	got, _ := fileIdentity(leaf)
	want, _ := bindDirectory(filepath.Join(dest, "Album", "CD1"))
	if got != want {
		t.Fatal("leaf identity")
	}
	again, err := openFolders(dir, []string{"Album", "CD1"}, true)
	if err != nil {
		t.Fatal("retry must reuse folders", err)
	}
	again.Close()
}

func TestOpenFoldersWithoutCreateRefusesMissing(t *testing.T) {
	dest, dir := openDest(t)
	if _, err := openFolders(dir, []string{"none"}, false); !errors.Is(err, syscall.ENOENT) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "none")); !os.IsNotExist(err) {
		t.Fatal("created without create")
	}
	root, err := openFolders(dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if got, _ := fileIdentity(root); got != mustIdentity(t, dir) {
		t.Fatal("empty folders must return the destination itself")
	}
}

func TestOpenFoldersRefusesSymlinkAndFileComponents(t *testing.T) {
	dest, dir := openDest(t)
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(dest, "link"))
	os.WriteFile(filepath.Join(dest, "file"), []byte("x"), 0600)
	for _, folders := range [][]string{{"link"}, {"link", "x"}, {"file"}, {"file", "x"}} {
		_, err := openFolders(dir, folders, true)
		if publishCode(err) != "destination-not-directory" {
			t.Fatal(folders, err)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("followed symlink out of the destination")
	}
}

func TestPublishCodeMapsFolderErrors(t *testing.T) {
	for err, want := range map[error]string{
		syscall.EEXIST:       "destination-exists",
		syscall.EACCES:       "PERMISSION_DENIED",
		syscall.EPERM:        "PERMISSION_DENIED",
		syscall.ENOSPC:       "NO_SPACE",
		syscall.ENOTDIR:      "destination-not-directory",
		syscall.ELOOP:        "destination-not-directory",
		errCrossDevice:       "destination-not-directory",
		syscall.ENAMETOOLONG: "name-too-long",
		syscall.EIO:          "",
	} {
		if got := publishCode(err); got != want {
			t.Errorf("%v: %q", err, got)
		}
	}
}

func TestPublishedPartFindsSubfolderName(t *testing.T) {
	dest, dir := openDest(t)
	os.MkdirAll(filepath.Join(dest, "Album", "CD1"), 0700)
	os.WriteFile(filepath.Join(dest, "part"), []byte("abc"), 0600)
	os.Link(filepath.Join(dest, "part"), filepath.Join(dest, "Album", "CD1", "01.flac"))
	di := mustIdentity(t, dir)
	part, _ := os.Open(filepath.Join(dest, "part"))
	pi, _ := fileIdentity(part)
	part.Close()
	op := Operation{Name: "Album/CD1/01.flac", Size: 3, Subject: Subject{UID: os.Getuid()}, Part: &partMetadata{Directory: di, Part: pi, Destination: dest}}
	if _, ok := (subprocessWriter{}).PublishedPart(context.Background(), op); !ok {
		t.Fatal("published subfolder part not recognized")
	}
	op.Name = "Album/CD2/01.flac"
	if _, ok := (subprocessWriter{}).PublishedPart(context.Background(), op); ok {
		t.Fatal("accepted a missing folder")
	}
	if _, err := os.Stat(filepath.Join(dest, "Album", "CD2")); !os.IsNotExist(err) {
		t.Fatal("recovery created a folder")
	}
}

func mustIdentity(t *testing.T, f *os.File) directoryIdentity {
	t.Helper()
	id, err := fileIdentity(f)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
