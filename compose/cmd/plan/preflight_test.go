package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/idgateway"

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
	if err := preflightProject(project, incoming, t.TempDir()); err != nil {
		t.Fatal("incoming preflight attempted snapshot refresh", err)
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

func TestInstalledPreflightRefreshesExternalOriginBeforeContainerStart(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	project := filepath.Join(root, "compose")
	os.Mkdir(project, 0700)
	os.Mkdir(filepath.Join(root, "sso"), 0700)
	os.Mkdir(filepath.Join(data, "sso"), 0700)
	cfg := idgateway.Config{Package: "Fixture", Mode: idgateway.ModeNone, Listen: idgateway.Listen{HTTPS: 18443}, StateDir: filepath.Join(data, "sso"), Upstream: "http://127.0.0.1:8000", AuthorizeCGI: "/webman/3rdparty/Fixture/sso/authorize.cgi", External: &idgateway.ExternalConfig{Connect: "none"}}
	raw, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(root, "sso/gateway.json"), raw, 0600)
	os.WriteFile(filepath.Join(project, "compose.lock.json"), []byte(`{"package":"Fixture","services":[{"storage_init":{"runtime":{"environment":{"DOMAIN":{"external_origin":true}}}}}]}`), 0600)
	for n, origin := range []string{"https://first.example.com", "https://second.example.com", ""} {
		raw, _ := json.Marshal(idgateway.ExternalState{Schema: 1, Revision: uint64(n + 1), ManualOrigin: origin})
		os.WriteFile(cfg.ExternalStatePath(), raw, 0600)
		if err := preflightProject(project, t.TempDir(), data); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(project, "settings/external-origin.value"))
		if err != nil || string(got) != origin {
			t.Fatal("restart retained the previous origin", string(got), err)
		}
	}
}
