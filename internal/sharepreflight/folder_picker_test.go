package sharepreflight

import (
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectedChildPreflightDetectsACLAndStaleSource(t *testing.T) {
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	project := t.TempDir()
	data := t.TempDir()
	source := filepath.Join(root, "volume1", "Media", "Projects", "Deep")
	if err = os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	lock, _ := json.Marshal(map[string]any{"package": account.Username, "install_shared_folder": map[string]string{"mode": "select", "key": "media", "permission": "rw", "picker": "folder"}})
	os.WriteFile(filepath.Join(project, "compose.lock.json"), lock, 0600)
	os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"schema":1,"shared_folders":{"media":"Media"},"shared_folder_paths":{"media":"Projects/Deep"}}`), 0600)
	env := []byte("SYNOPKGLAND_MOUNT_media='" + source + "'\n")
	os.WriteFile(filepath.Join(project, ".env"), env, 0600)
	if err = check(project, data, root); err != nil {
		t.Fatal(err)
	}
	os.Chmod(source, 0500)
	if os.Geteuid() != 0 {
		if err = check(project, data, root); err == nil || !strings.Contains(err.Error(), "File Station") {
			t.Fatalf("child denial: %v", err)
		}
		var permission *sharedfolder.PermissionError
		if !errors.As(err, &permission) || permission.Subpath != "Projects/Deep" {
			t.Fatalf("dialog lost child permission type: %v", err)
		}
	}
	os.Chmod(source, 0700)
	os.WriteFile(filepath.Join(project, ".env"), []byte("SYNOPKGLAND_MOUNT_media='/volume2/Media/Projects/Deep'\n"), 0600)
	if err = check(project, data, root); err == nil || !strings.Contains(err.Error(), "location changed") {
		t.Fatalf("stale source: %v", err)
	}
	os.WriteFile(filepath.Join(project, ".env"), env, 0600)
	os.Remove(source)
	if err = check(project, data, root); err == nil {
		t.Fatal("missing child accepted")
	}
}
