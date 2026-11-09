package main

import (
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
	"testing"
)

func TestFolderStatusExitDefersOnlyRecoverableFolderFailures(t *testing.T) {
	for _, err := range []error{&sharedfolder.PermissionError{Name: "Media", Subpath: "Projects", Need: sharedfolder.Write}, &sharedfolder.SelectionError{Name: "Media", Subpath: "Projects", Reason: "missing"}} {
		if code := folderFailureExitCode(fmt.Errorf("startup: %w", err)); code != 2 {
			t.Fatal(code)
		}
	}
	if code := folderFailureExitCode(fmt.Errorf("container manager unavailable")); code != 1 {
		t.Fatal(code)
	}
}
