package pkgstate

import (
	"path/filepath"
	"testing"
)

func TestDirectoryUsesPhysicalStateBeforeUpgradeSymlinkExists(t *testing.T) {
	t.Setenv("SYNOPKG_PKGVAR", "")
	t.Setenv("SYNOPKG_PKGDEST_VOL", "")
	if got := Directory("Example", ""); got != "/var/packages/Example/var" {
		t.Fatal(got)
	}
	volume := t.TempDir()
	t.Setenv("SYNOPKG_PKGDEST_VOL", volume)
	if got := Directory("Example", ""); got != filepath.Join(volume, "@appdata", "Example") {
		t.Fatal(got)
	}
	t.Setenv("SYNOPKG_PKGVAR", "/physical/package/data")
	if got := Directory("Example", ""); got != "/physical/package/data" {
		t.Fatal(got)
	}
	if got := Directory("Example", "/explicit/data"); got != "/explicit/data" {
		t.Fatal(got)
	}
}
