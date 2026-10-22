package sharepreflight

import (
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
)

func TestSelectedSharePreflightAndRetry(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("checks require a non-root account")
	}
	u, _ := user.Current()
	project, data, root := t.TempDir(), t.TempDir(), t.TempDir()
	path := filepath.Join(root, "volume1", "Documents")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0500)
	lock := map[string]any{"package": u.Username, "install_shared_folder": InstallSharedFolder{Mode: "select", Key: "consume", Permission: "rw"}}
	raw, _ := json.Marshal(lock)
	os.WriteFile(filepath.Join(project, "compose.lock.json"), raw, 0600)
	os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"schema":1,"shared_folders":{"consume":"Documents"}}`), 0600)
	// Even an INSTALL start must use saved configuration, not wizard variables.
	t.Setenv("SYNOPKG_PKG_STATUS", "INSTALL")
	t.Setenv("consume", "wrong wizard default")
	err := check(project, data, root)
	var perm *sharedfolder.PermissionError
	if !errors.As(err, &perm) || perm.Name != "Documents" || perm.Account != u.Username || perm.Need != sharedfolder.Write {
		t.Fatalf("missing permission: %v", err)
	}
	for _, part := range []string{"Control Panel > Shared Folder > Documents > Edit > Permissions > System internal user", u.Username, "Read/Write"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("missing %q: %v", part, err)
		}
	}
	os.Chmod(path, 0700)
	if err := check(project, data, root); err != nil {
		t.Fatalf("grant then retry: %v", err)
	}
	os.Chmod(path, 0500)
	lock["install_shared_folder"] = InstallSharedFolder{Mode: "select", Key: "consume", Permission: "ro"}
	raw, _ = json.Marshal(lock)
	os.WriteFile(filepath.Join(project, "compose.lock.json"), raw, 0600)
	if err := check(project, data, root); err != nil {
		t.Fatalf("read only: %v", err)
	}
	os.Remove(filepath.Join(data, "config.json"))
	if err := check(project, data, root); !errors.Is(err, sharedfolder.ErrNotConfigured) {
		t.Fatalf("missing selection: %v", err)
	}
}

func TestSharePreflightRejectsInvalidLockAndWrongIdentity(t *testing.T) {
	for _, raw := range []string{`{`, `{"package":"OtherAccount","install_shared_folder":{"mode":"select","key":"consume","permission":"rw"}}`, `{"package":"ExampleApp","install_shared_folder":{"mode":"select","key":"consume","permission":"none"}}`} {
		project := t.TempDir()
		os.WriteFile(filepath.Join(project, "compose.lock.json"), []byte(raw), 0600)
		if err := check(project, t.TempDir(), t.TempDir()); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	project := t.TempDir()
	os.WriteFile(filepath.Join(project, "compose.lock.json"), []byte(`{"package":"ExampleApp"}`), 0600)
	if err := check(project, t.TempDir(), t.TempDir()); err != nil {
		t.Fatalf("no selected shares: %v", err)
	}
}
