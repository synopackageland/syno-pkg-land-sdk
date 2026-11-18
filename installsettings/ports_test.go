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

func originPortFixture(t *testing.T) (string, string) {
	t.Helper()
	root, state := portFixture(t)
	cfg := PortConfig{Schema: 1, Package: "ExampleApp", Ports: map[string]Port{
		"gateway_http":   {Default: 28000, Protocol: "tcp"},
		"gateway_origin": {Default: 28443, Protocol: "tcp", Conflict: ConflictWarn},
	}, Files: map[string]string{
		"sso/gateway.json":      `{"listen":{"http":@@PORT_gateway_http@@},"origin":{"https":@@PORT_gateway_origin@@}}`,
		"sso/native-nginx.conf": "listen @@PORT_gateway_origin@@ ssl;",
	}}
	raw, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(root, "install/ports.json"), raw, 0600)
	os.WriteFile(filepath.Join(root, "sso/gateway.json"), []byte(`{"listen":{"http":28000},"origin":{"https":28443}}`), 0600)
	os.WriteFile(filepath.Join(root, "sso/native-nginx.conf"), []byte("listen 28443 ssl;"), 0600)
	return root, state
}

func TestWarnPolicyInstallsThroughAConflict(t *testing.T) {
	root, state := originPortFixture(t)
	t.Setenv("SYNOPKG_PKG_STATUS", "INSTALL")
	t.Setenv("pkgwizard_port_gateway_origin", "28500")
	conflict := func(_ context.Context, _, key string, _ Port, _ int) error {
		if key == "gateway_origin" {
			return ErrPort
		}
		return nil
	}
	var warned []string
	if err := preparePortsWarn(root, "ExampleApp", state, conflict, func(w string) { warned = append(warned, w) }); err != nil {
		t.Fatal(err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "gateway_origin") || !strings.Contains(warned[0], "28500") {
		t.Fatal(warned)
	}
	values, _ := PortValues(root)
	if values["gateway_origin"] != 28500 {
		t.Fatal(values)
	}
	// Format, range and duplicates still block a warn port.
	for _, raw := range []string{"80", "70000", "5001", "abc", "28000"} {
		root, state := originPortFixture(t)
		t.Setenv("pkgwizard_port_gateway_origin", raw)
		if err := preparePortsWarn(root, "ExampleApp", state, availablePort, func(string) {}); err == nil {
			t.Fatal(raw)
		}
	}
	// A port without the warn policy still blocks on a conflict.
	root, state = originPortFixture(t)
	t.Setenv("pkgwizard_port_gateway_origin", "")
	if err := preparePortsWarn(root, "ExampleApp", state, func(context.Context, string, string, Port, int) error { return ErrPort }, func(string) {}); !errors.Is(err, ErrPort) {
		t.Fatal(err)
	}
}

func TestPortConfigRejectsAnUnknownConflictPolicy(t *testing.T) {
	root, state := portFixture(t)
	raw := `{"schema":1,"package":"ExampleApp","ports":{"gateway_http":{"default":28000,"protocol":"tcp","conflict":"ignore"}},"files":{}}`
	os.WriteFile(filepath.Join(root, "install/ports.json"), []byte(raw), 0600)
	if err := preparePorts(root, "ExampleApp", state, availablePort); err == nil {
		t.Fatal("accepted")
	}
}

func TestChangePortsRewritesGeneratedFilesAndTheRememberedValue(t *testing.T) {
	root, state := originPortFixture(t)
	t.Setenv("SYNOPKG_PKG_STATUS", "INSTALL")
	if err := preparePorts(root, "ExampleApp", state, availablePort); err != nil {
		t.Fatal(err)
	}
	if err := RememberPorts(root, "ExampleApp", state); err != nil {
		t.Fatal(err)
	}
	conflict := func(_ context.Context, _, key string, _ Port, n int) error {
		if n == 28600 {
			return ErrPort
		}
		return nil
	}
	warnings, err := changePorts(root, "ExampleApp", state, map[string]int{"gateway_origin": 28600}, conflict)
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "28600") {
		t.Fatal(warnings, err)
	}
	for path, want := range map[string]string{
		"sso/gateway.json":      `{"listen":{"http":28000},"origin":{"https":28600}}`,
		"sso/native-nginx.conf": "listen 28600 ssl;",
	} {
		if b, _ := os.ReadFile(filepath.Join(root, path)); string(b) != want {
			t.Fatalf("%s: %s", path, b)
		}
	}
	sc, _ := os.ReadFile(filepath.Join(root, "install/ExampleApp.sc"))
	env, _ := os.ReadFile(filepath.Join(root, "install/ports.env"))
	if !strings.Contains(string(sc), `dst.ports="28600/tcp"`) || !strings.Contains(string(env), "SYNOPKGLAND_PORT_GATEWAY_ORIGIN=28600") || !strings.Contains(string(env), "SYNOPKGLAND_PORT_GATEWAY_HTTP=28000") {
		t.Fatal(string(sc), string(env))
	}
	// An upgrade keeps the changed value.
	t.Setenv("SYNOPKG_PKG_STATUS", "UPGRADE")
	if err := preparePorts(root, "ExampleApp", state, availablePort); err != nil {
		t.Fatal(err)
	}
	if values, _ := PortValues(root); values["gateway_origin"] != 28600 || values["gateway_http"] != 28000 {
		t.Fatal(values)
	}
}

func TestChangePortsRefusesInvalidRequests(t *testing.T) {
	root, state := originPortFixture(t)
	t.Setenv("SYNOPKG_PKG_STATUS", "INSTALL")
	if err := preparePorts(root, "ExampleApp", state, availablePort); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "sso/gateway.json"))
	conflict := func(context.Context, string, string, Port, int) error { return ErrPort }
	for name, change := range map[string]map[string]int{
		"undeclared":     {"gateway_https": 28700},
		"range":          {"gateway_origin": 80},
		"reserved":       {"gateway_origin": 5001},
		"duplicate":      {"gateway_origin": 28000},
		"empty":          {},
		"blocking clash": {"gateway_http": 28700},
	} {
		if _, err := changePorts(root, "ExampleApp", state, change, conflict); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(root, "sso/gateway.json")); string(after) != string(before) {
		t.Fatal("refused change rewrote the payload")
	}
}
