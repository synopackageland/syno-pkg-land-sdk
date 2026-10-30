package installsettings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func portFixture(t *testing.T) (string, string) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("package account required")
	}
	root, state := t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(root, "install"), 0700)
	os.Mkdir(filepath.Join(root, "sso"), 0700)
	cfg := PortConfig{Schema: 1, Package: "ExampleApp", Ports: map[string]Port{"gateway_http": {Default: 28000, Protocol: "tcp"}}, Files: map[string]string{"sso/gateway.json": `{"listen":{"http":@@PORT_gateway_http@@},"timer":28000}`}}
	raw, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(root, "install/ports.json"), raw, 0600)
	os.WriteFile(filepath.Join(root, "install/ExampleApp.sc"), nil, 0644)
	os.WriteFile(filepath.Join(root, "sso/gateway.json"), []byte(`{"listen":{"http":28000},"timer":28000}`), 0600)
	return root, state
}

var availablePort = func(context.Context, string, string, Port, int) error { return nil }

func TestPortSelectionFlowsToConfigServiceEnvironmentAndUpgrade(t *testing.T) {
	root, state := portFixture(t)
	t.Setenv("SYNOPKG_PKG_STATUS", "INSTALL")
	t.Setenv("pkgwizard_port_gateway_http", "28123")
	if err := preparePorts(root, "ExampleApp", state, availablePort); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "sso/gateway.json"))
	if string(raw) != `{"listen":{"http":28123},"timer":28000}` {
		t.Fatal(string(raw))
	}
	sc, _ := os.ReadFile(filepath.Join(root, "install/ExampleApp.sc"))
	if !strings.Contains(string(sc), `dst.ports="28123/tcp"`) {
		t.Fatal(string(sc))
	}
	env, _ := os.ReadFile(filepath.Join(root, "install/ports.env"))
	if !strings.Contains(string(env), "SYNOPKGLAND_PORT_GATEWAY_HTTP=28123") {
		t.Fatal(string(env))
	}
	if err := RememberPorts(root, "ExampleApp", state); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYNOPKG_PKG_STATUS", "UPGRADE")
	t.Setenv("pkgwizard_port_gateway_http", "28456")
	if err := preparePorts(root, "ExampleApp", state, availablePort); err != nil {
		t.Fatal(err)
	}
	values, err := PortValues(root)
	if err != nil || values["gateway_http"] != 28123 {
		t.Fatal(values, err)
	}
}
func TestPortsRefuseConflictsInvalidValuesAndEscapingTemplates(t *testing.T) {
	for _, raw := range []string{"5000", "443", "65536", "028000", "28000;id"} {
		root, state := portFixture(t)
		t.Setenv("pkgwizard_port_gateway_http", raw)
		if err := preparePorts(root, "ExampleApp", state, availablePort); err == nil {
			t.Fatal(raw)
		}
	}
	root, state := portFixture(t)
	t.Setenv("pkgwizard_port_gateway_http", "28123")
	conflict := func(context.Context, string, string, Port, int) error { return ErrPort }
	if err := preparePorts(root, "ExampleApp", state, conflict); !errors.Is(err, ErrPort) {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "sso/gateway.json"))
	if strings.Contains(string(b), "28123") {
		t.Fatal("failed check changed payload")
	}
	raw, _ := os.ReadFile(filepath.Join(root, "install/ports.json"))
	var cfg PortConfig
	json.Unmarshal(raw, &cfg)
	cfg.Files = map[string]string{"../outside": "bad"}
	raw, _ = json.Marshal(cfg)
	os.WriteFile(filepath.Join(root, "install/ports.json"), raw, 0600)
	if err := preparePorts(root, "ExampleApp", state, availablePort); err == nil {
		t.Fatal("escaping template accepted")
	}
}
