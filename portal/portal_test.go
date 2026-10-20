package portal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const appID = "SYNO.SDS.HelloGo.Instance"

// saved mirrors DSM 7.4's AppPortal.json after an administrator sets an alias.
func saved(alias string) string {
	return "{\n\t\"acl\" : \"\",\n\t\"alias\" : " + alias + ",\n\t\"alternatePort\" : null,\n\t\"application\" : \"Hello Go\",\n\t\"fqdn\" : null,\n\t\"https\" : \n\t{\n\t\t\"hsts\" : false\n\t},\n\t\"id\" : \"" + appID + "\",\n\t\"version\" : 3\n}\n"
}

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "AppPortal.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestActiveAlias(t *testing.T) {
	got := Read(write(t, saved(`"hello-go"`)), appID)
	if got != (Status{State: Active, Alias: "hello-go"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestNoSavedSettingIsInactive(t *testing.T) {
	if got := Read(filepath.Join(t.TempDir(), "AppPortal.json"), appID); got != (Status{State: Inactive}) {
		t.Fatalf("missing file: %+v", got)
	}
	for _, alias := range []string{`null`, `""`} {
		if got := Read(write(t, saved(alias)), appID); got != (Status{State: Inactive}) {
			t.Fatalf("alias %s: %+v", alias, got)
		}
	}
}

func TestAnythingElseFailsClosed(t *testing.T) {
	other := strings.Replace(saved(`"hello-go"`), appID, "SYNO.SDS.Other.Instance", 1)
	cases := map[string]string{
		"another app":     other,
		"no id":           `{"alias":"hello-go"}`,
		"duplicate alias": `{"alias":"hello-go","alias":"evil","id":"` + appID + `"}`,
		"duplicate id":    `{"id":"SYNO.SDS.Other.Instance","alias":"hello-go","id":"` + appID + `"}`,
		"alias number":    saved(`1`),
		"alias path":      saved(`"a/b"`),
		"alias dots":      saved(`".."`),
		"alias query":     saved(`"a?b"`),
		"alias too long":  saved(`"` + strings.Repeat("a", 65) + `"`),
		"array":           `[]`,
		"trailing value":  saved(`"hello-go"`) + `{}`,
		"not json":        `alias=hello-go`,
		"not utf8":        "{\"alias\":\"\xff\",\"id\":\"" + appID + "\"}",
		"too large":       saved(`"hello-go"`) + strings.Repeat(" ", MaxConfigBytes),
		"too deep":        strings.Replace(saved(`"hello-go"`), `"acl" : ""`, `"acl" : `+strings.Repeat("[", 64)+strings.Repeat("]", 64), 1),
	}
	for name, content := range cases {
		if got := Read(write(t, content), appID); got != (Status{State: Unavailable}) {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestOnlyARegularFileIsRead(t *testing.T) {
	dir := t.TempDir()
	target := write(t, saved(`"hello-go"`))
	link := filepath.Join(dir, "AppPortal.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if got := Read(link, appID); got != (Status{State: Unavailable}) {
		t.Fatalf("symlink: %+v", got)
	}
	if got := Read(dir, appID); got != (Status{State: Unavailable}) {
		t.Fatalf("directory: %+v", got)
	}
}

func TestConfigPathIsThePackagesOwnSetting(t *testing.T) {
	if got := ConfigPath("HelloGo"); got != "/var/packages/HelloGo/etc/AppPortal.json" {
		t.Fatal(got)
	}
}
