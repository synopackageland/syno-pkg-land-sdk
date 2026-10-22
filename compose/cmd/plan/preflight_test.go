package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
)

func TestPreflightSkipsOnlyIncomingWizardNotInstallMarkedStart(t *testing.T) {
	incoming := t.TempDir()
	project := filepath.Join(incoming, "compose")
	os.Mkdir(project, 0700)
	os.WriteFile(filepath.Join(project, "compose.lock.json"), []byte(`{"package":"OtherAccount","install_shared_folder":{"mode":"select","key":"consume","permission":"rw"}}`), 0600)
	if err := preflightShares(project, incoming, t.TempDir()); err != nil {
		t.Fatal("incoming wizard must install before grant", err)
	}
	// DSM may keep INSTALL and wizard environment values for the first start.
	t.Setenv("SYNOPKG_PKG_STATUS", "INSTALL")
	t.Setenv("consume", "prefilled share")
	if err := preflightShares(project, t.TempDir(), t.TempDir()); !errors.Is(err, sharedfolder.ErrIdentity) {
		t.Fatalf("installed start bypassed identity check: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "incoming")
	os.Symlink(incoming, alias)
	if err := preflightShares(project, alias, t.TempDir()); err != nil {
		t.Fatal("incoming alias", err)
	}
}
