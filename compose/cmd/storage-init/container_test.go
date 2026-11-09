package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerCommandSurface(t *testing.T) {
	var out, errb bytes.Buffer
	if code := containerCommand([]string{"--help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "commands for container Apps") || !strings.Contains(out.String(), "fix-permissions") {
		t.Fatalf("help %d %q", code, out.String())
	}
	if code := containerCommand([]string{"unknown"}, &out, &errb); code != 2 {
		t.Fatal("unknown subcommand accepted")
	}
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".netrc"), []byte("x"), 0644)
	os.Chmod(filepath.Join(home, ".netrc"), 0644)
	out.Reset()
	if code := containerCommand([]string{"fix-permissions", "--home", home}, &out, &errb); code != 0 || !strings.Contains(out.String(), "tightened "+filepath.Join(home, ".netrc")) {
		t.Fatalf("fix-permissions %d %q %q", code, out.String(), errb.String())
	}
	if st, _ := os.Stat(filepath.Join(home, ".netrc")); st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	if code := containerCommand([]string{"init", "--config", "{}"}, &out, &errb); code != 1 {
		t.Fatal("init without argv accepted")
	}
}
