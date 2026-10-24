package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/idgateway"
	"github.com/synopackageland/syno-pkg-land-sdk/installsettings"
)

func fallbackFixture(t *testing.T) (string, string, []Inventory, idgateway.Config, string) {
	t.Helper()
	project, pkgvar, _, cfg := externalOriginFixture(t)
	cfg.PublicOrigin = ""
	cfg.External = &idgateway.ExternalConfig{Connect: "none"}
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(project, "../sso/gateway.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	values := filepath.Join(project, "../install/values")
	if err := os.MkdirAll(values, 0700); err != nil {
		t.Fatal(err)
	}
	services := []Inventory{{StorageInit: &StorageInitialization{Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"DOMAIN": {ExternalOrigin: true, Setting: "public_origin"}}}}}}
	return project, pkgvar, services, cfg, filepath.Join(values, "public_origin.value")
}

func TestExternalOriginFallbackSelectsHostValue(t *testing.T) {
	for _, tc := range []struct{ name, external, fallback, want string }{
		{"external wins", "https://external.example.com", "https://local.example.com:18443", "https://external.example.com"},
		{"fallback", "", "https://local.example.com:18443", "https://local.example.com:18443"},
		{"empty", "", "", ""},
		{"unused invalid fallback", "https://external.example.com", "invalid", "https://external.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, pkgvar, services, cfg, setting := fallbackFixture(t)
			os.WriteFile(setting, []byte(tc.fallback), 0600)
			state, _ := json.Marshal(idgateway.ExternalState{Schema: 1, Revision: 1, ManualOrigin: tc.external})
			os.WriteFile(cfg.ExternalStatePath(), state, 0600)
			if err := prepareRuntimeSecrets(project, "Fixture", pkgvar, services); err != nil {
				t.Fatal(err)
			}
			value, err := readExternalOriginSnapshot(filepath.Join(project, "settings/external-origin.value"))
			if err != nil || string(value) != tc.want {
				t.Fatalf("snapshot %q, want %q: %v", value, tc.want, err)
			}
			t.Setenv("DOMAIN", "old")
			if err := loadRuntimeEnvironmentAt(services[0].StorageInit.Runtime.Environment, project); err != nil {
				t.Fatal(err)
			}
			got, present := os.LookupEnv("DOMAIN")
			if got != tc.want || present != (tc.want != "") {
				t.Fatalf("container value %q present=%v", got, present)
			}
		})
	}
}

func TestExternalOriginFallbackMissingOptionalValueStaysUnset(t *testing.T) {
	project, pkgvar, services, _, _ := fallbackFixture(t)
	if err := prepareRuntimeSecrets(project, "Fixture", pkgvar, services); err != nil {
		t.Fatal(err)
	}
	value, err := readExternalOriginSnapshot(filepath.Join(project, "settings/external-origin.value"))
	if err != nil || len(value) != 0 {
		t.Fatal(string(value), err)
	}
}

func TestExternalOriginFallbackRejectsUnsafeValuesWithoutReplacingSnapshot(t *testing.T) {
	for _, kind := range []string{"invalid", "http", "query", "credentials", "control", "oversize", "public", "symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			project, pkgvar, services, _, setting := fallbackFixture(t)
			const valid = "https://local.example.com:18443"
			os.WriteFile(setting, []byte(valid), 0600)
			if err := prepareRuntimeSecrets(project, "Fixture", pkgvar, services); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "public":
				os.Chmod(setting, 0644)
			case "symlink", "hardlink":
				other := filepath.Join(t.TempDir(), "origin")
				os.WriteFile(other, []byte(valid), 0600)
				os.Remove(setting)
				if kind == "symlink" {
					os.Symlink(other, setting)
				} else {
					os.Link(other, setting)
				}
			default:
				bad := map[string]string{"invalid": "invalid", "http": "http://local.example.com", "query": valid + "?q=1", "credentials": "https://user:pass@local.example.com", "control": valid + "\n", "oversize": strings.Repeat("x", 2049)}[kind]
				os.WriteFile(setting, []byte(bad), 0600)
			}
			if err := prepareRuntimeSecrets(project, "Fixture", pkgvar, services); err == nil {
				t.Fatal("unsafe fallback accepted")
			}
			value, err := readExternalOriginSnapshot(filepath.Join(project, "settings/external-origin.value"))
			if err != nil || string(value) != valid {
				t.Fatal("failed refresh replaced snapshot", string(value), err)
			}
		})
	}
}

func TestExternalOriginFallbackPlannerAndInventory(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Data = nil
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/server']\n    volumes: ['state:/tmp']\nvolumes:\n  state: {}\n"), 0600)
	r.Services["openlist"] = ServicePolicy{StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/tmp", Path: "app"}}, Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"DOMAIN": {ExternalOrigin: true, Setting: "public_origin"}, "PUBLIC_URL": {ExternalOrigin: true, Setting: "public_origin"}}}}}
	if _, err := Build(context.Background(), r); err == nil {
		t.Fatal("undeclared fallback accepted")
	}
	r.InstallSettings = map[string]installsettings.Setting{"public_origin": {Type: "https-origin", Label: "Origin", Required: false}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range p.Effective.Services["openlist"].Volumes {
		if strings.Contains(m.Source, "/install/values/") {
			t.Fatal("installation value mounted in container", m)
		}
		if strings.HasPrefix(m.Target, "/synopkgland/settings/") {
			count++
			if m.Source != "/var/packages/Fixture/target/compose/settings/external-origin.value" || m.Target != "/synopkgland/settings/external-origin.value" || !m.ReadOnly || m.Bind == nil || m.Bind.CreateHostPath != false {
				t.Fatal(m)
			}
		}
	}
	if count != 1 {
		t.Fatal("snapshot mount count", count)
	}
	project, pkgvar, _, cfg, setting := fallbackFixture(t)
	os.WriteFile(setting, []byte("https://local.example.com:18443"), 0600)
	lock, _ := json.Marshal(struct {
		Package  string      `json:"package"`
		Services []Inventory `json:"services"`
	}{"Fixture", p.Services})
	os.WriteFile(filepath.Join(project, "compose.lock.json"), lock, 0600)
	for n, external := range []string{"", "https://external.example.com", ""} {
		state, _ := json.Marshal(idgateway.ExternalState{Schema: 1, Revision: uint64(n + 1), ManualOrigin: external})
		os.WriteFile(cfg.ExternalStatePath(), state, 0600)
		if err := RefreshExternalOrigin(project, pkgvar); err != nil {
			t.Fatal(err)
		}
		want := external
		if want == "" {
			want = "https://local.example.com:18443"
		}
		got, err := readExternalOriginSnapshot(filepath.Join(project, "settings/external-origin.value"))
		if err != nil || string(got) != want {
			t.Fatal("inventory lost fallback", string(got), err)
		}
	}
	// A shared snapshot cannot represent distinct fallback policies.
	r.Services["openlist"].StorageInit.Runtime.Environment["PUBLIC_URL"] = RuntimeEnvironment{ExternalOrigin: true}
	if _, err := Build(context.Background(), r); err == nil {
		t.Fatal("planner accepted conflicting fallback policies")
	}
}

func TestExternalOriginFallbackRejectsInvalidRefreshInventory(t *testing.T) {
	for _, entry := range []RuntimeEnvironment{{ExternalOrigin: true, Setting: "../origin"}, {ExternalOrigin: true, Setting: "public_origin", Secret: "key"}, {ExternalOrigin: true, Setting: "public_origin", OIDCClient: "client"}} {
		project, pkgvar, services, _, _ := fallbackFixture(t)
		services[0].StorageInit.Runtime.Environment["DOMAIN"] = entry
		lock, _ := json.Marshal(struct {
			Package  string      `json:"package"`
			Services []Inventory `json:"services"`
		}{"Fixture", services})
		os.WriteFile(filepath.Join(project, "compose.lock.json"), lock, 0600)
		if err := RefreshExternalOrigin(project, pkgvar); err == nil {
			t.Fatalf("unsafe inventory accepted: %+v", entry)
		}
	}
}

func TestExternalOriginFallbackRefreshesSettingAndUnset(t *testing.T) {
	project, pkgvar, services, _, setting := fallbackFixture(t)
	lock, _ := json.Marshal(struct {
		Package  string      `json:"package"`
		Services []Inventory `json:"services"`
	}{"Fixture", services})
	os.WriteFile(filepath.Join(project, "compose.lock.json"), lock, 0600)
	for _, value := range []string{"https://first.example.com:18443", "https://second.example.com:18443", ""} {
		os.WriteFile(setting, []byte(value), 0600)
		if err := RefreshExternalOrigin(project, pkgvar); err != nil {
			t.Fatal(err)
		}
		got, err := readExternalOriginSnapshot(filepath.Join(project, "settings/external-origin.value"))
		if err != nil || string(got) != value {
			t.Fatal("restart kept the previous setting", string(got), err)
		}
	}
	os.WriteFile(setting, []byte("http://invalid.example.com"), 0600)
	if err := RefreshExternalOrigin(project, pkgvar); err == nil {
		t.Fatal("restart accepted an invalid fallback")
	}
}

func TestExternalOriginFallbackRequiresOnePolicyAcrossServices(t *testing.T) {
	for _, second := range []string{"public_origin", "other_origin", ""} {
		project, pkgvar, services, _, setting := fallbackFixture(t)
		os.WriteFile(setting, []byte("https://local.example.com:18443"), 0600)
		services = append(services, Inventory{StorageInit: &StorageInitialization{Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"OTHER_URL": {ExternalOrigin: true, Setting: second}}}}})
		err := prepareRuntimeSecrets(project, "Fixture", pkgvar, services)
		if (err == nil) != (second == "public_origin") {
			t.Fatalf("fallback %q: %v", second, err)
		}
		lock, _ := json.Marshal(struct {
			Package  string      `json:"package"`
			Services []Inventory `json:"services"`
		}{"Fixture", services})
		os.WriteFile(filepath.Join(project, "compose.lock.json"), lock, 0600)
		err = RefreshExternalOrigin(project, pkgvar)
		if (err == nil) != (second == "public_origin") {
			t.Fatalf("installed fallback %q: %v", second, err)
		}
	}
}
