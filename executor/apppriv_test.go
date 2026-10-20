package main

import (
	"bytes"
	"debug/elf"
	"errors"
	"os"
	"testing"
)

// The executor ships the CGI's SLIBAppPrivUserHas bridge byte for byte; the
// C source and its reproducible build stay in ../dsm/native.
func TestAppPrivBridgesAreTheCGIBridges(t *testing.T) {
	for arch, want := range map[string]struct {
		file    string
		machine elf.Machine
	}{"amd64": {"x86_64", elf.EM_X86_64}, "arm64": {"aarch64", elf.EM_AARCH64}, "arm": {"armv7", elf.EM_ARM}} {
		cgi, err := os.ReadFile("../dsm/native/apppriv-" + want.file + ".elf")
		if err != nil || !bytes.Equal(appPrivBridges[arch], cgi) {
			t.Fatalf("%s bridge differs from the CGI's: %v", arch, err)
		}
		file, err := elf.NewFile(bytes.NewReader(appPrivBridges[arch]))
		if err != nil || file.Machine != want.machine {
			t.Fatalf("%s bridge: %v %v", arch, file, err)
		}
	}
	if len(appPrivBridges) != 3 {
		t.Fatal("bridges", len(appPrivBridges))
	}
}

func TestAppPrivBridgeAnswerIsExactlyOneBit(t *testing.T) {
	for out, want := range map[string]bool{"1\n": true, "0\n": false} {
		if got, err := parseAppPriv([]byte(out), nil); err != nil || got != want {
			t.Fatalf("%q: %v %v", out, got, err)
		}
	}
	for _, out := range []string{"", "1", "2\n", "1\n1\n", " 1\n", "true\n"} {
		if got, err := parseAppPriv([]byte(out), nil); err == nil || got {
			t.Fatalf("%q accepted", out)
		}
	}
	if got, err := parseAppPriv([]byte("1\n"), errors.New("exit 2")); err == nil || got {
		t.Fatal("a failed bridge allowed")
	}
}
