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
	if _, err := nativeShareEnvironment(f, t.TempDir(), root); err != nil {
		t.Fatalf("preinst must not require write access: %v", err)
	}
	os.Chmod(filepath.Dir(source), 0000)
	defer os.Chmod(filepath.Dir(source), 0755)
	if _, err := nativeShareEnvironment(f, t.TempDir(), root); err != nil {
		t.Fatalf("pre-grant traversal denial must be deferred: %v", err)
	}
}
