package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/idgateway"

	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
)

func TestHeadlessStopCheckSharesFailureAndRecoverySemantics(t *testing.T) {
	t.Setenv("SYNOPKG_PKG_STATUS", "STOP")
	if err := stopCheck(); err == nil || !strings.Contains(err.Error(), "COMPOSE_STOP_UNCONFIRMED") || !strings.Contains(err.Error(), "Manual Install") {
		t.Fatalf("missing worker inventory must fail with recovery: %v", err)
	}
	for _, status := range []string{"INSTALL", "UPGRADE", "UNINSTALL"} {
		t.Setenv("SYNOPKG_PKG_STATUS", status)
		if err := stopCheck(); err != nil {
			t.Fatalf("stop check blocked %s recovery: %v", status, err)
		}
	}
}

func TestWorkerProfileWaitDoesNotBlockProfileGeneratingWorkers(t *testing.T) {
	incoming := t.TempDir()
	project := filepath.Join(incoming, "compose")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"INSTALL", "UPGRADE", "UNINSTALL", "STOP", ""} {
		if workerProfileWaitRequired(project, "", status) {
			t.Fatalf("profile generation would deadlock for %q", status)
		}
	}
	if workerProfileWaitRequired(project, incoming, "START") || !workerProfileWaitRequired(project, "", "START") {
		t.Fatal("START must wait only in the installed project")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(incoming, alias); err != nil {
		t.Fatal(err)
	}
	if workerProfileWaitRequired(project, alias, "START") {
		t.Fatal("incoming alias must not wait for profiles")
	}
}

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
