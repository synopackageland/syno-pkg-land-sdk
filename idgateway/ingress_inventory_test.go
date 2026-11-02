package idgateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inventoryFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"usr/syno/synoman/webman", "usr/syno/etc/www/app.d", "var/packages", "usr/local/etc/nginx/conf.d"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func inventoryWrite(t *testing.T, root, path, data string) {
	t.Helper()
	file := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}
func TestIngressPathValidation(t *testing.T) {
	for _, path := range []string{"", "/synopkgland/App/", "/reader/", "/my-app/ui2/"} {
		if err := validateIngressPath("App", path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
	for _, path := range []string{"/", "/Reader/", "/webman/", "/webapi/api/", "/scripts/", "/synohdpack/", "/synopkgland/Other/", "/synopkgland/app/", "/reader", "//reader/", "/a/../b/", "/a/%2e/", "/a_b/", "/a/?x", "/a/#x", "/a/\\b/", "/" + strings.Repeat("a", 65) + "/"} {
		if err := validateIngressPath("App", path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
}
func TestIngressInventoryConflictsAndOwnUpgrade(t *testing.T) {
	cases := []struct{ name, path, data string }{
		{"native directory", "usr/syno/synoman/reader/index.html", ""},
		{"saved alias", "usr/syno/etc/www/app.d/Other.json", `{"alias":"reader"}`},
		{"stopped portal", "var/packages/Other/target/etc/portal.json", `{"alias":"reader"}`},
		{"stopped gateway", "var/packages/Other/target/sso/native-nginx.conf", `location ^~ /reader/ { }`},
		{"active literal", "usr/local/etc/nginx/conf.d/dsm.other.conf", `location = /reader { }`},
		{"deep regex", "usr/local/etc/nginx/conf.d/dsm.other.conf", `location ~ ^/reader/(api|data)/ { }`},
		{"active regex", "usr/local/etc/nginx/conf.d/dsm.other.conf", `location ~ ^/reader/ { }`},
		{"alias regex", "usr/local/etc/nginx/conf.d/alias.other.conf", `location ~* ^/READER/ { }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := inventoryFixture(t)
			inventoryWrite(t, root, c.path, c.data)
			if err := checkIngressOwnership(root, "App", "/reader/"); err == nil {
				t.Fatal("accepted a foreign path")
			}
			if err := checkIngressOwnership(root, "App", "/reading/"); err != nil {
				t.Fatal(err)
			}
		})
	}
	root := inventoryFixture(t)
	inventoryWrite(t, root, "var/packages/App/target/sso/native-nginx.conf", `location ^~ /reader/ { }`)
	inventoryWrite(t, root, "usr/local/etc/nginx/conf.d/dsm.pkg-static.App-123.conf", `location ^~ /reader/ { }`)
	for _, prefix := range []string{"/reader/", "/synopkgland/App/"} {
		if err := checkIngressOwnership(root, "App", prefix); err != nil {
			t.Fatal(err)
		}
	}
	// Siblings do not collide, but either ancestor direction does.
	if !pathsOverlap("/reader/ui/", "/reader/") || !pathsOverlap("/reader/", "/reader/ui/") || pathsOverlap("/reader/ui/", "/reader/api/") {
		t.Fatal("segment overlap")
	}
}
func TestIngressInventoryFailsClosed(t *testing.T) {
	root := inventoryFixture(t)
	inventoryWrite(t, root, "usr/syno/etc/www/app.d/bad.json", `{`)
	if err := checkIngressOwnership(root, "App", "/reader/"); err == nil {
		t.Fatal("accepted corrupt alias")
	}
	if err := checkIngressOwnership(t.TempDir(), "App", "/reader/"); err == nil {
		t.Fatal("accepted absent inventory")
	}
	root = inventoryFixture(t)
	inventoryWrite(t, root, "usr/local/etc/nginx/conf.d/dsm.other.conf", `location ~ ^/other/(?!a) { }`)
	if err := checkIngressOwnership(root, "App", "/other/"); err == nil {
		t.Fatal("accepted unknown regex")
	}
}
