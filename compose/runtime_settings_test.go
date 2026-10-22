package compose

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/installsettings"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeSettingDeclarationIsNotARandomSecret(t *testing.T) {
	var i StorageInitialization
	if err := json.Unmarshal([]byte(`{"directories":[{"base":"/data","path":"app"}],"runtime":{"environment":{"DOMAIN":{"setting":"public-origin"}}}}`), &i); err != nil {
		t.Fatal(err)
	}
	if err := validateRuntime(i); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSettingReaderIsBoundedAndPrivate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "public-origin.value")
	for _, value := range []string{"https://vault.example:18443", ""} {
		if err := os.WriteFile(p, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := readRuntimeSetting(p)
		if value == "" {
			if err == nil {
				t.Fatal("empty setting accepted")
			}
		} else if err != nil || string(got) != value {
			t.Fatalf("setting: %q %v", got, err)
		}
	}
	os.WriteFile(p, []byte("https://vault.example"), 0644)
	os.Chmod(p, 0644)
	if _, err := readRuntimeSetting(p); err == nil {
		t.Fatal("public mode accepted")
	}
	os.Remove(p)
	os.Symlink("/etc/passwd", p)
	if _, err := readRuntimeSetting(p); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestRuntimeSettingRejectsControlOversizeAndInvalidUTF8(t *testing.T) {
	for _, value := range []string{"a\nb", strings.Repeat("x", 2049), string([]byte{0xff, 0xfe})} {
		p := filepath.Join(t.TempDir(), "setting.value")
		os.WriteFile(p, []byte(value), 0600)
		if _, err := readRuntimeSetting(p); err == nil {
			t.Fatal("invalid setting accepted")
		}
	}
}

func TestRuntimeSettingPrepareRequiresNativeInstallationValue(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "compose")
	os.Mkdir(project, 0700)
	services := []Inventory{{StorageInit: &StorageInitialization{Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"DOMAIN": {Setting: "public_origin"}}}}}}
	if err := prepareRuntimeSecrets(project, "Fixture", t.TempDir(), services); err == nil {
		t.Fatal("missing native setting accepted")
	}
	dir := filepath.Join(root, "install/values")
	os.MkdirAll(dir, 0700)
	file := filepath.Join(dir, "public_origin.value")
	os.WriteFile(file, []byte("https://vault.example"), 0600)
	if err := prepareRuntimeSecrets(project, "Fixture", t.TempDir(), services); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSettingDeclarationRejectsAmbiguousSources(t *testing.T) {
	for _, entry := range []RuntimeEnvironment{{Setting: "../escape"}, {Setting: "origin", Secret: "key"}, {Setting: "origin", OIDCClient: "client"}} {
		i := StorageInitialization{Directories: []WritableDirectory{{Base: "/data", Path: "app"}}, Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"DOMAIN": entry}}}
		if err := validateRuntime(i); err == nil {
			t.Fatal("ambiguous source accepted")
		}
	}
}

func TestRuntimeSettingPlannerRequiresDeclarationAndUsesOnlyNativePayload(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Data = nil
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/server']\n    volumes: ['state:/tmp']\nvolumes:\n  state: {}\n"), 0600)
	r.Services["openlist"] = ServicePolicy{StorageInit: &StorageInitialization{Directories: []WritableDirectory{{Base: "/tmp", Path: "app"}}, Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"DOMAIN": {Setting: "public_origin"}}}}}
	if _, err := Build(context.Background(), r); err == nil {
		t.Fatal("undeclared setting accepted")
	}
	r.InstallSettings = map[string]installsettings.Setting{"public_origin": {Type: "https-origin", Label: "Public origin", Required: true}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range p.Effective.Services["openlist"].Volumes {
		if m.Target == "/synopkgland/settings/public_origin.value" {
			found = m.ReadOnly && m.Source == "/var/packages/Fixture/target/install/values/public_origin.value" && m.Bind.CreateHostPath == false
		}
	}
	if !found {
		t.Fatal("native setting mount missing")
	}
}
