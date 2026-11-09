package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFolderPickerEnvironment(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "volume1", "Media", "Projects", "Deep")
	os.MkdirAll(source, 0755)
	f := &InstallSharedFolder{Mode: "select", Key: "media", Permission: "rw", Picker: "folder"}
	t.Setenv("SYNOPKG_PKG_STATUS", "INSTALL")
	t.Setenv("media", "Media")
	t.Setenv("media_subpath", "Projects/Deep")
	env, err := nativeShareEnvironment(f, t.TempDir(), root)
	if err != nil || !strings.Contains(env, source) {
		t.Fatalf("%s %v", env, err)
	}
	for _, bad := range []string{"../outside", "missing", "Projects//Deep"} {
		t.Setenv("media_subpath", bad)
		if _, err := nativeShareEnvironment(f, t.TempDir(), root); err == nil {
			t.Fatal(bad)
		}
	}
	t.Setenv("media_subpath", "Projects/Deep")
	os.Chmod(source, 0500)
	if os.Geteuid() != 0 {
		if _, err := nativeShareEnvironment(f, t.TempDir(), root); err == nil || !strings.Contains(err.Error(), "No ACL was changed") {
			t.Fatalf("write denial: %v", err)
		}
	}
}
