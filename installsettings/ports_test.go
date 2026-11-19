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
		"sso/gateway.json":             `{"listen":{"http":@@PORT_gateway_http@@},"origin":{"https":@@PORT_gateway_origin@@}}`,
		"sso/native-origin-nginx.conf": "listen @@PORT_gateway_origin@@ ssl;",
	}}
	raw, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(root, "install/ports.json"), raw, 0600)
	os.WriteFile(filepath.Join(root, "sso/gateway.json"), []byte(`{"listen":{"http":28000},"origin":{"https":28443}}`), 0600)
	os.WriteFile(filepath.Join(root, "sso/native-origin-nginx.conf"), []byte("listen 28443 ssl;"), 0600)
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

func TestChangePortsLeavesInstalledFilesUntilInstallation(t *testing.T) {
	for _, phase := range []string{"INSTALL", "UPGRADE"} {
		t.Run(phase, func(t *testing.T) {
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
			assertPortStatus(t, root, state, "gateway_origin", PortStatus{Configured: 28600, Effective: 28443, Pending: true})
			for path, want := range map[string]string{
				"sso/gateway.json":             `{"listen":{"http":28000},"origin":{"https":28443}}`,
				"sso/native-origin-nginx.conf": "listen 28443 ssl;",
			} {
				if b, _ := os.ReadFile(filepath.Join(root, path)); string(b) != want {
					t.Fatalf("%s: %s", path, b)
				}
			}
			sc, _ := os.ReadFile(filepath.Join(root, "install/ExampleApp.sc"))
			env, _ := os.ReadFile(filepath.Join(root, "install/ports.env"))
			if !strings.Contains(string(sc), `dst.ports="28443/tcp"`) || !strings.Contains(string(env), "SYNOPKGLAND_PORT_GATEWAY_ORIGIN=28443") || !strings.Contains(string(env), "SYNOPKGLAND_PORT_GATEWAY_HTTP=28000") {
				t.Fatal(string(sc), string(env))
			}
			if values, err := PortValues(root); err != nil || values["gateway_origin"] != 28443 {
				t.Fatal(values, err)
			}
			// Model the incoming payload, separately from the running installation.
			incoming, _ := originPortFixture(t)
			t.Setenv("SYNOPKG_PKG_STATUS", phase)
			t.Setenv("pkgwizard_port_gateway_origin", "28443")
			if err := preparePorts(incoming, "ExampleApp", state, availablePort); err != nil {
				t.Fatal(err)
			}
			if values, _ := PortValues(incoming); values["gateway_origin"] != 28600 || values["gateway_http"] != 28000 {
				t.Fatal(values)
			}
			if b, _ := os.ReadFile(filepath.Join(incoming, "sso/native-origin-nginx.conf")); string(b) != "listen 28600 ssl;" {
				t.Fatalf("incoming nginx was not regenerated: %s", b)
			}
			if err := RememberPorts(incoming, "ExampleApp", state); err != nil {
				t.Fatal(err)
			}
			assertPortStatus(t, incoming, state, "gateway_origin", PortStatus{Configured: 28600, Effective: 28600})
		})
	}
}

func assertPortStatus(t *testing.T, root, state, key string, want PortStatus) {
	t.Helper()
	status, err := ReadPortStatus(root, "ExampleApp", state)
	if err != nil || status[key] != want {
		t.Fatalf("status[%s] = %+v, %v; want %+v", key, status[key], err, want)
	}
}

func TestPortStatusAndSuccessivePendingChanges(t *testing.T) {
	root, state := originPortFixture(t)
	if err := preparePorts(root, "ExampleApp", state, availablePort); err != nil {
		t.Fatal(err)
	}
	// No retained state yet: the installed selection is the configured value.
	assertPortStatus(t, root, state, "gateway_origin", PortStatus{Configured: 28443, Effective: 28443})
	for _, change := range []map[string]int{
		{"gateway_origin": 28600},
		{"gateway_http": 28100},
	} {
		if _, err := changePorts(root, "ExampleApp", state, change, availablePort); err != nil {
			t.Fatal(err)
		}
	}
	assertPortStatus(t, root, state, "gateway_origin", PortStatus{Configured: 28600, Effective: 28443, Pending: true})
	assertPortStatus(t, root, state, "gateway_http", PortStatus{Configured: 28100, Effective: 28000, Pending: true})
	// Validate against the whole configured selection, including pending ports.
	if _, err := changePorts(root, "ExampleApp", state, map[string]int{"gateway_http": 28600}, availablePort); err == nil {
		t.Fatal("duplicate pending port accepted")
	}
	assertPortStatus(t, root, state, "gateway_http", PortStatus{Configured: 28100, Effective: 28000, Pending: true})
	if _, err := changePorts(root, "ExampleApp", state, map[string]int{"gateway_origin": 28443}, availablePort); err != nil {
		t.Fatal(err)
	}
	assertPortStatus(t, root, state, "gateway_origin", PortStatus{Configured: 28443, Effective: 28443})
}

func TestPortStatusRefusesCorruptRetainedValues(t *testing.T) {
	root, state := originPortFixture(t)
	if err := preparePorts(root, "ExampleApp", state, availablePort); err != nil {
		t.Fatal(err)
	}
	if err := RememberPorts(root, "ExampleApp", state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "install-ports/gateway_origin.value")
	if err := os.WriteFile(path, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPortStatus(root, "ExampleApp", state); err == nil {
		t.Fatal("corrupt pending state hidden")
	}
	if _, err := changePorts(root, "ExampleApp", state, map[string]int{"gateway_http": 28100}, availablePort); err == nil {
		t.Fatal("corrupt pending state overwritten")
	}
}

func TestPendingHTTPSPortUpdatesRetainedOriginOnlyDuringInstallation(t *testing.T) {
	root, state := portFixture(t)
	files := map[string]string{
		"install/settings.json": `{"schema":1,"package":"ExampleApp","https_port":@@PORT_gateway_https@@,"settings":{"public_origin":{"type":"https-origin","label":"Public origin","required":true},"external_origin":{"type":"external-origin","label":"External origin"}}}`,
		"sso/native-nginx.conf": "listen @@PORT_gateway_https@@ ssl;",
	}
	cfg := PortConfig{Schema: 1, Package: "ExampleApp", Ports: map[string]Port{
		"gateway_https": {Default: 28443, Protocol: "tcp"},
	}, Files: files}
	makePayload := func(payload string) {
		t.Helper()
		raw, _ := json.Marshal(cfg)
		if err := os.WriteFile(filepath.Join(payload, "install/ports.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		for path := range files {
			if err := os.WriteFile(filepath.Join(payload, path), []byte("placeholder"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := preparePorts(payload, "ExampleApp", state, availablePort); err != nil {
			t.Fatal(err)
		}
	}
	makePayload(root)
	t.Setenv("pkgwizard_setting_public_origin", "https://app.example:28443")
	t.Setenv("pkgwizard_setting_external_origin", "https://external.example")
	if err := Prepare(root, "ExampleApp", state); err != nil {
		t.Fatal(err)
	}
	if err := Remember(root, "ExampleApp", state); err != nil {
		t.Fatal(err)
	}
	if err := RememberPorts(root, "ExampleApp", state); err != nil {
		t.Fatal(err)
	}
	if _, err := changePorts(root, "ExampleApp", state, map[string]int{"gateway_https": 28600}, availablePort); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "install-settings/public_origin.value")); string(b) != "https://app.example:28443" {
		t.Fatalf("pending change rewrote the effective origin: %s", b)
	}
	incoming, _ := portFixture(t)
	t.Setenv("SYNOPKG_PKG_STATUS", "UPGRADE")
	t.Setenv("pkgwizard_setting_public_origin", "")
	t.Setenv("pkgwizard_setting_external_origin", "")
	makePayload(incoming)
	if err := Prepare(incoming, "ExampleApp", state); err != nil {
		t.Fatalf("saved origin prevented pending HTTPS port installation: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(incoming, "install/values/public_origin.value")); string(b) != "https://app.example:28600" {
		t.Fatalf("origin does not match the incoming listener: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(incoming, "install/values/external_origin.value")); string(b) != "https://external.example" {
		t.Fatalf("external origin changed: %s", b)
	}
	// Explicit installer input is validated as entered, never silently rebound.
	t.Setenv("pkgwizard_setting_public_origin", "https://app.example:28443")
	if err := Prepare(incoming, "ExampleApp", state); err == nil {
		t.Fatal("explicit mismatched origin accepted")
	}
	t.Setenv("pkgwizard_setting_public_origin", "")
	if err := os.WriteFile(filepath.Join(state, "install-settings/public_origin.value"), []byte("https://user@app.example:28443"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(incoming, "ExampleApp", state); err == nil {
		t.Fatal("malformed retained origin rebound")
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

// The CLI writes these files with @@PORT_<key>@@ placeholders. portConfig runs
// no ownership check, so this guard also runs under root, where the
// preparation tests above skip: a template the allowlist does not admit makes
// every install and upgrade of such an App fail in preinst.
func TestPortConfigAdmitsEveryGeneratedPortTemplate(t *testing.T) {
	generated := []string{
		"sso/gateway.json",
		"sso/native-nginx.conf",
		"sso/native-origin-nginx.conf",
		"ui/config",
		"ui/window-0003.js",
		"daemon/config.json",
		"compose/request.json",
		"install/settings.json",
	}
	files := map[string]string{}
	for _, path := range generated {
		if !portPath(path) {
			t.Errorf("port template %s is not admitted", path)
		}
		files[path] = "@@PORT_gateway_origin@@"
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "install"), 0700)
	raw, _ := json.Marshal(PortConfig{Schema: 1, Package: "ExampleApp", Ports: map[string]Port{
		"gateway_origin": {Default: 5443, Protocol: "tcp", Conflict: ConflictWarn},
	}, Files: files})
	os.WriteFile(filepath.Join(dir, "install/ports.json"), raw, 0600)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := portConfig(root, "ExampleApp"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"sso/other.conf", "../sso/gateway.json", "ui/../x.js", "etc/nginx.conf"} {
		if portPath(path) {
			t.Errorf("%s admitted", path)
		}
	}
}
