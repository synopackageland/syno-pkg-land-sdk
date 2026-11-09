package sharepreflight

import (
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func TestNativeSelectionResolvesMovedShareAndRefusesBrokenSelections(t *testing.T) {
	account, _ := user.Current()
	payload, data, root := t.TempDir(), t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(payload, "install"), 0700)
	cfg := NativeConfig{Schema: 1, Package: account.Username, Folder: &InstallSharedFolder{Mode: "select", Key: "media", Permission: "rw", Picker: "folder"}}
	raw, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(payload, "install/folder.json"), raw, 0600)
	// Unit-runner usernames may contain underscores; use the same package-name
	// contract as the manifest rather than manufacturing a DSM account.
	if !packageName.MatchString(account.Username) {
		t.Skip("test runner name is not a package ID")
	}
	source := filepath.Join(root, "volume1", "Media", "Projects", "Deep")
	os.MkdirAll(source, 0700)
	record := []byte(`{"schema":1,"shared_folders":{"media":"Media"},"shared_folder_paths":{"media":"Projects/Deep"}}`)
	os.WriteFile(filepath.Join(data, "config.json"), record, 0600)
	path, err := NativePath(payload, data, "media", root)
	if err != nil || path != source {
		t.Fatalf("%q %v", path, err)
	}
	os.Mkdir(filepath.Join(root, "volume2"), 0700)
	os.Rename(filepath.Join(root, "volume1", "Media"), filepath.Join(root, "volume2", "Media"))
	moved := filepath.Join(root, "volume2", "Media", "Projects", "Deep")
	if path, err = NativePath(payload, data, "media", root); err != nil || path != moved {
		t.Fatalf("moved %q %v", path, err)
	}
	os.Chmod(moved, 0500)
	if _, err = NativePath(payload, data, "media", root); !errors.Is(err, sharedfolder.ErrPermissionPending) {
		t.Fatalf("permission: %v", err)
	}
	os.Chmod(moved, 0700)
	os.Rename(moved, moved+"Original")
	os.Symlink(moved+"Original", moved)
	var selection *sharedfolder.SelectionError
	if _, err = NativePath(payload, data, "media", root); !errors.As(err, &selection) {
		t.Fatalf("symlink: %v", err)
	}
	if _, err = NativePath(payload, data, "other", root); !errors.Is(err, sharedfolder.ErrNotConfigured) {
		t.Fatalf("undeclared key: %v", err)
	}
	os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"schema":1,"shared_folders":{"media":"Media"},"shared_folder_paths":{}}`), 0600)
	if _, err = NativePath(payload, data, "media", root); err == nil {
		t.Fatal("missing subpath became root")
	}
}

func TestNativePreparationChecksStructureBeforeGrantAndRetainsUpgrade(t *testing.T) {
	account, _ := user.Current()
	if !packageName.MatchString(account.Username) {
		t.Skip("runner name is not a package ID")
	}
	payload, data, root := t.TempDir(), t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(payload, "install"), 0700)
	cfg := NativeConfig{Schema: 1, Package: account.Username, Folder: &InstallSharedFolder{Mode: "dedicated", Name: "Media", Key: "media", Permission: "rw", Picker: "folder"}}
	raw, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(payload, "install/folder.json"), raw, 0600)
	parent := filepath.Join(root, "volume1", "Media", "Projects")
	os.MkdirAll(filepath.Join(parent, "Deep"), 0700)
	t.Setenv("SYNOPKG_PKG_STATUS", "INSTALL")
	t.Setenv("media", "Media")
	t.Setenv("media_subpath", "Projects/Deep")
	os.Chmod(parent, 0000)
	if err := PrepareNative(payload, data, account.Username, root); err != nil {
		t.Fatalf("pre-grant access: %v", err)
	}
	os.Chmod(parent, 0700)
	t.Setenv("media_subpath", "../escape")
	if err := PrepareNative(payload, data, account.Username, root); err == nil {
		t.Fatal("traversal accepted")
	}
	t.Setenv("media_subpath", "Projects/Missing")
	if err := PrepareNative(payload, data, account.Username, root); err == nil {
		t.Fatal("missing folder accepted")
	}
	os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"schema":1,"shared_folders":{"media":"Media"},"shared_folder_paths":{"media":"Projects/Deep"}}`), 0600)
	t.Setenv("SYNOPKG_PKG_STATUS", "UPGRADE")
	os.Unsetenv("media")
	os.Unsetenv("media_subpath")
	if err := PrepareNative(payload, data, account.Username, root); err != nil {
		t.Fatalf("retain: %v", err)
	}
	t.Setenv("SYNOPKG_PKG_STATUS", "INSTALL")
	t.Setenv("media", "Media")
	t.Setenv("media_subpath", "")
	t.Setenv("SYNOPKG_PKGINST_TEMP_DIR", payload)
	os.Rename(filepath.Join(root, "volume1", "Media"), filepath.Join(root, "volume1", "Previous"))
	if err := PrepareNative(payload, data, account.Username, root); err != nil {
		t.Fatalf("dedicated root: %v", err)
	}
	t.Setenv("media_subpath", "Missing")
	if err := PrepareNative(payload, data, account.Username, root); err == nil {
		t.Fatal("created missing dedicated subfolder")
	}
}

func TestNativeSilentLegacyUpgradeRetainsShareRoot(t *testing.T) {
	account, _ := user.Current()
	if !packageName.MatchString(account.Username) {
		t.Skip("runner name is not a package ID")
	}
	payload, data, root := t.TempDir(), t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(payload, "install"), 0700)
	cfg := NativeConfig{Schema: 1, Package: account.Username, Folder: &InstallSharedFolder{Mode: "select", Key: "media", Permission: "rw", Picker: "folder"}}
	raw, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(payload, "install/folder.json"), raw, 0600)
	os.MkdirAll(filepath.Join(root, "volume1", "Media"), 0700)
	os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"schema":1,"shared_folders":{"media":"Media"}}`), 0600)
	t.Setenv("SYNOPKG_PKG_STATUS", "UPGRADE")
	os.Unsetenv("media")
	os.Unsetenv("media_subpath")
	if err := PrepareNative(payload, data, account.Username, root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("media", "Media")
	if err := PrepareNative(payload, data, account.Username, root); err != nil {
		t.Fatalf("DSM saved variable: %v", err)
	}
	path, err := NativePath(payload, data, "media", root)
	if err != nil || path != filepath.Join(root, "volume1", "Media") {
		t.Fatalf("%q %v", path, err)
	}
}

func TestNativeSilentUpgradeBeforeVarSymlinkExists(t *testing.T) {
	account, _ := user.Current()
	if !packageName.MatchString(account.Username) {
		t.Skip("runner name is not a package ID")
	}
	payload, volume, root := t.TempDir(), t.TempDir(), t.TempDir()
	data := filepath.Join(volume, "@appdata", account.Username)
	os.MkdirAll(data, 0700)
	os.Mkdir(filepath.Join(payload, "install"), 0700)
	cfg := NativeConfig{Schema: 1, Package: account.Username, Folder: &InstallSharedFolder{Mode: "select", Key: "media", Permission: "rw", Picker: "folder"}}
	raw, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(payload, "install/folder.json"), raw, 0600)
	os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"schema":1,"shared_folders":{"media":"Media"}}`), 0600)
	os.MkdirAll(filepath.Join(root, "volume1", "Media"), 0700)
	t.Setenv("SYNOPKG_PKG_STATUS", "UPGRADE")
	t.Setenv("SYNOPKG_PKGVAR", "")
	t.Setenv("SYNOPKG_PKGDEST_VOL", volume)
	os.Unsetenv("media")
	os.Unsetenv("media_subpath")
	if err := PrepareNative(payload, "", account.Username, root); err != nil {
		t.Fatal(err)
	}
}
