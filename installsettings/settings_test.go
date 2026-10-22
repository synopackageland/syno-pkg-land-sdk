package installsettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	root, state := t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(root, "install"), 0700)
	b, _ := json.Marshal(Config{Schema: 1, Package: "Fixture", HTTPSPort: 29443, Settings: map[string]Setting{"public_origin": {Type: "https-origin", Label: "Public HTTPS origin", Required: true}}})
	os.WriteFile(filepath.Join(root, "install/settings.json"), b, 0600)
	return root, state
}

func TestOriginAdmission(t *testing.T) {
	for _, value := range []string{"https://vault.example:29443", "https://192.0.2.1:29443"} {
		if err := ValidateOrigin(value, 29443); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"", "http://vault.example:29443", "https://vault.example", "https://vault.example:443", "https://vault.example:29443/", "https://vault.example:29443/a", "https://u@vault.example:29443", "https://vault.example:29443?", "https://vault.example:29443#", "https://vault.example:29443\n", "https://a..example:29443", "https://-a.example:29443", "https://[::1]:29443", "https://a:029443"} {
		if err := ValidateOrigin(value, 29443); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	if err := ValidateOrigin("https://vault.example", 0); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleFailsClosedAndPreservesUpgradeAndReinstall(t *testing.T) {
	root, state := fixture(t)
	t.Setenv("pkgwizard_setting_public_origin", "")
	if err := Prepare(root, "Fixture", state); err == nil {
		t.Fatal("missing required accepted")
	}
	t.Setenv("pkgwizard_setting_public_origin", "https://vault.example:29443")
	if err := Prepare(root, "Fixture", state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "install/values/public_origin.value")
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	if err := Remember(root, "Fixture", state); err != nil {
		t.Fatal(err)
	}
	next, _ := fixture(t)
	t.Setenv("pkgwizard_setting_public_origin", "")
	if err := Prepare(next, "Fixture", state); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(next, "install/values/public_origin.value"))
	if string(b) != "https://vault.example:29443" {
		t.Fatal("lost retained setting")
	}
	t.Setenv("pkgwizard_setting_public_origin", "https://bad.example:443")
	if err := Prepare(next, "Fixture", state); err == nil {
		t.Fatal("invalid input fell back to retained")
	}
	b, _ = os.ReadFile(filepath.Join(next, "install/values/public_origin.value"))
	if string(b) != "https://vault.example:29443" {
		t.Fatal("failed validation modified value")
	}
	t.Setenv("SYNOPKG_PKG_STATUS", "UPGRADE")
	t.Setenv("pkgwizard_remove_additional", "true")
	if err := Purge("Fixture", state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "install-settings/public_origin.value")); err != nil {
		t.Fatal("upgrade purged setting")
	}
	t.Setenv("SYNOPKG_PKG_STATUS", "")
	t.Setenv("pkgwizard_remove_additional", "false")
	if err := Purge("Fixture", state); err != nil {
		t.Fatal(err)
	}
	t.Setenv("pkgwizard_remove_additional", "true")
	if err := Purge("Fixture", state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "install-settings")); !os.IsNotExist(err) {
		t.Fatal("delete-data retained setting")
	}
}

func TestRejectSymlinkAndNonprivateRetainedState(t *testing.T) {
	for _, kind := range []string{"directory-symlink", "file-symlink", "permissions", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			root, state := fixture(t)
			t.Setenv("pkgwizard_setting_public_origin", "")
			dir := filepath.Join(state, "install-settings")
			os.Mkdir(dir, 0700)
			file := filepath.Join(dir, "public_origin.value")
			os.WriteFile(file, []byte("https://vault.example:29443"), 0600)
			switch kind {
			case "directory-symlink":
				os.RemoveAll(dir)
				os.Symlink(t.TempDir(), dir)
			case "file-symlink":
				os.Remove(file)
				os.Symlink(filepath.Join(t.TempDir(), "outside"), file)
			case "permissions":
				os.Chmod(file, 0644)
			case "hardlink":
				os.Link(file, filepath.Join(dir, "other"))
			}
			if err := Prepare(root, "Fixture", state); err == nil {
				t.Fatal("unsafe retained state accepted")
			}
		})
	}
}

func TestDestinationVolumeFallbackAndAbsentStateRoot(t *testing.T) {
	root, state := fixture(t)
	t.Setenv("pkgwizard_setting_public_origin", "https://vault.example:29443")
	absent := filepath.Join(state, "absent")
	if err := Prepare(root, "Fixture", absent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("created DSM state root")
	}
	if err := Remember(root, "Fixture", absent); err == nil {
		t.Fatal("remember created DSM state root")
	}
	volume := t.TempDir()
	physical := filepath.Join(volume, "@appdata/Fixture")
	os.MkdirAll(physical, 0700)
	t.Setenv("SYNOPKG_PKGVAR", "")
	t.Setenv("SYNOPKG_PKGDEST_VOL", volume)
	if err := Remember(root, "Fixture", ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("pkgwizard_setting_public_origin", "")
	next, _ := fixture(t)
	if err := Prepare(next, "Fixture", ""); err != nil {
		t.Fatal(err)
	}
}
