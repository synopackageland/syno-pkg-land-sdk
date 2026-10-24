package installsettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExternalOriginRulesAndDSMSeparation(t *testing.T) {
	for _, raw := range []string{"https://APP.example.com", "https://app.example.com:443"} {
		if canonical, err := CanonicalExternalOrigin(raw); err != nil || canonical != "https://app.example.com" {
			t.Fatal(raw, canonical, err)
		}
		if err := ValidateExternalOrigin(raw, "https://app.example.com:443"); err == nil {
			t.Fatal("same DSM origin admitted", raw)
		}
		if err := ValidateExternalOrigin(raw, "https://dsm.example.com"); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"http://app.example.com", "https://app.example.com:18443", "https://192.0.2.1", "https://[::1]", "https://app.example.com/", "https://app.example.com?", "https://app.example.com#", "https://u@app.example.com", "https://app.example.com\n"} {
		if err := ValidateExternalOrigin(raw, ""); err == nil {
			t.Fatal(raw)
		}
	}
}
func TestExternalOriginLifecycleCanonicalizesAndRemainsOptional(t *testing.T) {
	root, state := fixture(t)
	config := Config{Schema: 1, Package: "Fixture", HTTPSPort: 29443, Settings: map[string]Setting{"manual_origin": {Type: "external-origin", Label: "Public address"}}}
	raw, _ := json.Marshal(config)
	os.WriteFile(filepath.Join(root, "install/settings.json"), raw, 0600)
	t.Setenv("pkgwizard_setting_manual_origin", "")
	if err := Prepare(root, "Fixture", state); err != nil {
		t.Fatal("optional", err)
	}
	if _, err := os.Stat(filepath.Join(root, "install/values/manual_origin.value")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	t.Setenv("pkgwizard_setting_manual_origin", "https://APP.example.com:443")
	if err := Prepare(root, "Fixture", state); err != nil {
		t.Fatal(err)
	}
	if err := Remember(root, "Fixture", state); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(state, "install-settings/manual_origin.value"))
	if err != nil || string(raw) != "https://app.example.com" {
		t.Fatal(string(raw), err)
	}
	t.Setenv("pkgwizard_setting_manual_origin", "https://app.example.com:29443")
	if err := Prepare(root, "Fixture", state); err == nil {
		t.Fatal("App port accepted for public URL")
	}
}
