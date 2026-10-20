//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadIntoCopiesARegularFile(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "docs"), 0700)
	os.WriteFile(filepath.Join(root, "docs", "a.txt"), []byte("abc"), 0600)
	var out bytes.Buffer
	n, err := readInto(root, "docs/a.txt", &out, 3, nil)
	if err != nil || n != 3 || out.String() != "abc" {
		t.Fatal(n, err, out.String())
	}
	if _, err := readInto(root, "docs/a.txt", &out, 2, nil); readCode(err) != "limit-exceeded" {
		t.Fatal(err)
	}
}

func TestReadIntoRefusesSymlinksFoldersAndMissingFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0600)
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "file-link"))
	os.Symlink(outside, filepath.Join(root, "dir-link"))
	os.Mkdir(filepath.Join(root, "folder"), 0700)
	syscall.Mkfifo(filepath.Join(root, "fifo"), 0600)
	for relative, want := range map[string]string{
		"file-link":       "invalid-path",
		"dir-link/secret": "invalid-path",
		"folder":          "not-a-file",
		"fifo":            "not-a-file",
		"missing":         "not-found",
	} {
		var out bytes.Buffer
		if _, err := readInto(root, relative, &out, maxReadBytes, nil); readCode(err) != want || out.Len() != 0 {
			t.Errorf("%s: %v", relative, err)
		}
	}
}

func TestReadCodeLeavesOtherErrorsUncoded(t *testing.T) {
	if readCode(syscall.EIO) != "" || readCode(nil) != "" || !strings.Contains(readRefusedError{"x"}.Error(), "x") {
		t.Fatal("codes")
	}
}
