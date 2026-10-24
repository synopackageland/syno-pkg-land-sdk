package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/idgateway"
)

func TestExternalOriginEnvironmentDeclaration(t *testing.T) {
	for _, e := range []RuntimeEnvironment{{ExternalOrigin: true}, {ExternalOrigin: true, Secret: "session"}, {ExternalOrigin: true, Setting: "origin"}, {ExternalOrigin: true, OIDCClient: "reader"}, {ExternalOrigin: true, Setting: "origin", Secret: "session"}, {ExternalOrigin: true, Setting: "origin", OIDCClient: "reader"}, {ExternalOrigin: true, Setting: "../origin"}} {
		i := StorageInitialization{Directories: []WritableDirectory{{Base: "/tmp", Path: "app"}}, Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"DOMAIN": e}}}
		err := validateRuntime(i)
		if (e.Secret == "" && e.OIDCClient == "" && e.Setting != "../origin") != (err == nil) {
			t.Fatalf("%+v: %v", e, err)
		}
	}
}

func TestExternalOriginPlannerUsesOnePrivateReadOnlyFileWithoutInstallSetting(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Data = nil
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/server']\n    volumes: ['state:/tmp']\nvolumes:\n  state: {}\n"), 0600)
	r.Services["openlist"] = ServicePolicy{StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/tmp", Path: "app"}}, Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"DOMAIN": {ExternalOrigin: true}, "PUBLIC_URL": {ExternalOrigin: true}}}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range p.Effective.Services["openlist"].Volumes {
		if m.Target == "/synopkgland/settings/external-origin.value" {
			count++
			if !m.ReadOnly || m.Source != "/var/packages/Fixture/target/compose/settings/external-origin.value" || m.Bind == nil || m.Bind.CreateHostPath != false {
				t.Fatal(m)
			}
		}
	}
	if count != 1 {
		t.Fatal("missing or duplicate external origin mount", count)
	}
	if strings.Contains(p.YAML, "https://") {
		t.Fatal("origin leaked to plan")
	}
}

func TestExternalOriginRuntimeEnvironmentSetsAndClearsInheritedValue(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "settings"), 0700)
	file := filepath.Join(root, "settings/external-origin.value")
	t.Setenv("DOMAIN", "inherited")
	values := map[string]RuntimeEnvironment{"DOMAIN": {ExternalOrigin: true}}
	for _, value := range []string{"https://app.example.com", ""} {
		os.WriteFile(file, []byte(value), 0600)
		if err := loadRuntimeEnvironmentAt(values, root); err != nil {
			t.Fatal(err)
		}
		got, present := os.LookupEnv("DOMAIN")
		if got != value || present != (value != "") {
			t.Fatalf("value=%q present=%v", got, present)
		}
	}
	os.Remove(file)
	if err := loadRuntimeEnvironmentAt(values, root); err == nil {
		t.Fatal("missing prepared snapshot accepted")
	}
}

func externalOriginFixture(t *testing.T) (string, string, []Inventory, idgateway.Config) {
	t.Helper()
	root, pkgvar := t.TempDir(), t.TempDir()
	project := filepath.Join(root, "compose")
	os.Mkdir(project, 0700)
	os.Mkdir(filepath.Join(root, "sso"), 0700)
	os.Mkdir(filepath.Join(pkgvar, "sso"), 0700)
	cfg := idgateway.Config{Package: "Fixture", Mode: idgateway.ModeNone, Listen: idgateway.Listen{HTTPS: 18443}, StateDir: filepath.Join(pkgvar, "sso"), Upstream: "http://127.0.0.1:8000", AuthorizeCGI: "/webman/3rdparty/Fixture/sso/authorize.cgi", PublicOrigin: "https://local.example.com:18443"}
	b, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(root, "sso/gateway.json"), b, 0600)
	services := []Inventory{{StorageInit: &StorageInitialization{Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"DOMAIN": {ExternalOrigin: true}}}}}}
	return project, pkgvar, services, cfg
}

func TestExternalOriginHostSnapshotChangesOnlyAtPrepare(t *testing.T) {
	project, pkgvar, services, cfg := externalOriginFixture(t)
	snapshot := filepath.Join(project, "settings/external-origin.value")
	if err := prepareRuntimeSecrets(project, "Fixture", pkgvar, services); err != nil {
		t.Fatal(err)
	}
	if b, err := readExternalOriginSnapshot(snapshot); err != nil || string(b) != cfg.PublicOrigin {
		t.Fatal(string(b), err)
	}
	state := idgateway.ExternalState{Schema: 1, Revision: 1, ManualOrigin: "https://manual.example.com"}
	b, _ := json.Marshal(state)
	os.WriteFile(cfg.ExternalStatePath(), b, 0600)
	if b, _ := os.ReadFile(snapshot); string(b) != cfg.PublicOrigin {
		t.Fatal("snapshot changed before restart")
	}
	first, _ := os.Stat(snapshot)
	if err := prepareRuntimeSecrets(project, "Fixture", pkgvar, services); err != nil {
		t.Fatal(err)
	}
	next, _ := os.Stat(snapshot)
	if os.SameFile(first, next) || next.Mode().Perm() != 0600 {
		t.Fatal("snapshot was not atomically replaced with a private file")
	}
	if b, err := readExternalOriginSnapshot(snapshot); err != nil || string(b) != state.ManualOrigin {
		t.Fatal(string(b), err)
	}
}

func TestExternalOriginWithoutValuePreparesEmptyOptionalSnapshot(t *testing.T) {
	project, pkgvar, services, cfg := externalOriginFixture(t)
	cfg.PublicOrigin = ""
	cfg.External = &idgateway.ExternalConfig{Connect: "none"}
	b, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(project, "../sso/gateway.json"), b, 0600)
	if err := prepareRuntimeSecrets(project, "Fixture", pkgvar, services); err != nil {
		t.Fatal("an origin must not be required", err)
	}
	if b, err := readExternalOriginSnapshot(filepath.Join(project, "settings/external-origin.value")); err != nil || len(b) != 0 {
		t.Fatal(b, err)
	}
	if _, err := os.Stat(filepath.Join(project, "secrets")); !os.IsNotExist(err) {
		t.Fatal("external origin was treated as a random secret")
	}
}

func TestExternalOriginSnapshotRefusesUnsafeDirectoriesAndFiles(t *testing.T) {
	project, pkgvar, services, _ := externalOriginFixture(t)
	other := t.TempDir()
	os.Symlink(other, filepath.Join(project, "settings"))
	if err := prepareRuntimeSecrets(project, "Fixture", pkgvar, services); err == nil {
		t.Fatal("symlink snapshot directory accepted")
	}
	if files, _ := os.ReadDir(other); len(files) != 0 {
		t.Fatal("wrote outside snapshot directory")
	}
	for _, content := range []string{"", "https://app.example.com"} {
		file := filepath.Join(t.TempDir(), "origin.value")
		os.WriteFile(file, []byte(content), 0644)
		if _, err := readExternalOriginSnapshot(file); err == nil {
			t.Fatal("world-readable snapshot accepted")
		}
		os.Chmod(file, 0600)
		os.Symlink(file, file+".link")
		if _, err := readExternalOriginSnapshot(file + ".link"); err == nil {
			t.Fatal("symlink snapshot accepted")
		}
	}
}
