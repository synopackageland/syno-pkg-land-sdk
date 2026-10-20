package main

import (
	"os"
	"path/filepath"
	"testing"
)

// runningExecutor installs runtime/executor and keeps it open, as a live monitor.
func runningExecutor(t *testing.T) (string, *os.File) {
	t.Helper()
	runtime := t.TempDir()
	path := filepath.Join(runtime, "executor")
	if err := os.WriteFile(path, []byte("old"), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return runtime, f
}

// replaceExecutor renames a new copy over runtime/executor, as control.sh and
// installAnchored do; the running inode is unlinked.
func replaceExecutor(t *testing.T, runtime, staged string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(runtime, staged), []byte("new"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(runtime, staged), filepath.Join(runtime, "executor")); err != nil {
		t.Fatal(err)
	}
}

func stat(t *testing.T, f *os.File) os.FileInfo {
	t.Helper()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestApprovedExecutableAcceptsTheLiveRuntimeExecutor(t *testing.T) {
	runtime, f := runningExecutor(t)
	if !approvedExecutable(filepath.Join(runtime, "executor"), stat(t, f), runtime, trustAll) {
		t.Fatal("live runtime executor refused")
	}
}

// Kernel 3.10 (DSM 7.1 armv7) names a rename-replaced inode after the rename
// source: control.sh stages .executor, installAnchored stages .executor-*.
func TestApprovedExecutableAcceptsAReplacedExecutorUnderItsSwappedName(t *testing.T) {
	for _, staged := range []string{".executor", ".executor-123456"} {
		runtime, f := runningExecutor(t)
		replaceExecutor(t, runtime, staged)
		for _, name := range []string{staged, "executor"} {
			link := filepath.Join(runtime, name) + " (deleted)"
			if !approvedExecutable(link, stat(t, f), runtime, trustAll) {
				t.Fatalf("replaced executor refused: %s", link)
			}
		}
	}
}

func TestApprovedExecutableRefusesAnotherInode(t *testing.T) {
	runtime, _ := runningExecutor(t)
	other := filepath.Join(runtime, ".executor")
	if err := os.WriteFile(other, []byte("other"), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(other)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	running := stat(t, f)
	// Still linked: not the installed executor, whatever the link claims.
	for _, link := range []string{other, other + " (deleted)", filepath.Join(runtime, "executor"), filepath.Join(runtime, "executor") + " (deleted)"} {
		if approvedExecutable(link, running, runtime, trustAll) {
			t.Fatalf("linked non-executor accepted as %s", link)
		}
	}
}

func TestApprovedExecutableRefusesAnUnlinkedInodeOutsideTheRuntime(t *testing.T) {
	runtime, f := runningExecutor(t)
	replaceExecutor(t, runtime, ".executor")
	running := stat(t, f)
	for _, link := range []string{
		filepath.Join(t.TempDir(), "executor") + " (deleted)",
		filepath.Join(runtime, "sh") + " (deleted)",
		filepath.Join(runtime, ".executor"), // unlinked yet not reported deleted
	} {
		if approvedExecutable(link, running, runtime, trustAll) {
			t.Fatalf("unlinked inode accepted as %s", link)
		}
	}
}

func TestApprovedExecutableRefusesAnUntrustedInode(t *testing.T) {
	runtime, f := runningExecutor(t)
	refuse := func(os.FileInfo) bool { return false }
	if approvedExecutable(filepath.Join(runtime, "executor"), stat(t, f), runtime, refuse) {
		t.Fatal("untrusted live executor accepted")
	}
	replaceExecutor(t, runtime, ".executor")
	if approvedExecutable(filepath.Join(runtime, ".executor")+" (deleted)", stat(t, f), runtime, refuse) {
		t.Fatal("untrusted replaced executor accepted")
	}
}
