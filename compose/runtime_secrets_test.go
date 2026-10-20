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

func TestRuntimeOIDCSecretsPreparedOnceAndOnlyDeclaredClientMounted(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "compose")
	os.Mkdir(project, 0700)
	os.Mkdir(filepath.Join(root, "sso"), 0700)
	os.WriteFile(filepath.Join(root, "sso/gateway.json"), []byte(`{"package":"Fixture","mode":"oidc","oidc":{"clients":[{"id":"reader"}]}}`), 0600)
	i := &StorageInitialization{Directories: []WritableDirectory{{Base: "/tmp", Path: "app"}}, Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"CLIENT_SECRET": {OIDCClient: "reader"}, "CRYPTO_KEY": {Secret: "session"}}}}
	services := []Inventory{{Service: "reader", StorageInit: i}}
	state := t.TempDir()
	if err := prepareRuntimeSecrets(project, "Fixture", state, services); err != nil {
		t.Fatal(err)
	}
	seedPath := filepath.Join(project, "secrets/oidc-reader.secret")
	first, err := os.ReadFile(seedPath)
	if err != nil || len(first) != 64 {
		t.Fatal("secret not generated", err)
	}
	got, err := idgateway.AdoptClientSecret(filepath.Join(state, "sso"), "reader", seedPath)
	if err != nil || got != string(first) {
		t.Fatal("provider did not adopt prepared client secret", err)
	}
	os.Remove(seedPath)
	if err := prepareRuntimeSecrets(project, "Fixture", state, services); err != nil {
		t.Fatal(err)
	}
	next, _ := os.ReadFile(seedPath)
	if string(next) != string(first) {
		t.Fatal("upgrade rotated client secret")
	}
	i.Runtime.Environment["UNREGISTERED"] = RuntimeEnvironment{OIDCClient: "other"}
	if err := prepareRuntimeSecrets(project, "Fixture", state, services); err == nil {
		t.Fatal("unregistered client accepted")
	}
}

func TestRuntimeSecretPlannerBindsOnlyPackagePayloadAndNoValue(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Data = nil
	r.IdentityMode = "oidc"
	r.Network.Egress = "internet"
	r.Web.HostPort = 0
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/server']\n    volumes: ['state:/tmp']\nvolumes:\n  state: {}\n"), 0600)
	r.Services["openlist"] = ServicePolicy{Network: "host", AddressEnv: "HOST", StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/tmp", Path: "app"}}, Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"SECRET": {OIDCClient: "reader"}}}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range p.Effective.Services["openlist"].Volumes {
		if m.Target == "/synopkgland/secrets/oidc-reader.secret" {
			found = m.ReadOnly && m.Source == "/var/packages/Fixture/target/compose/secrets/oidc-reader.secret" && m.Bind != nil && m.Bind.CreateHostPath == false
		}
	}
	if !found {
		t.Fatal("missing bounded secret mount")
	}
	lock, _ := p.LockJSON()
	var v any
	if json.Unmarshal(lock, &v) != nil || strings.Contains(string(lock), "secret-value") {
		t.Fatal("invalid lock")
	}
}
