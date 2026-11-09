package sharepreflight

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/pkgstate"
	"os"
	"path/filepath"

	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
)

type NativeConfig struct {
	Schema  int                  `json:"schema"`
	Package string               `json:"package"`
	Folder  *InstallSharedFolder `json:"sharedFolder"`
}

func nativeConfig(payload string) (NativeConfig, error) {
	var cfg NativeConfig
	raw, err := os.ReadFile(filepath.Join(payload, "install/folder.json"))
	if err != nil {
		return cfg, err
	}
	if len(raw) > 16384 || json.Unmarshal(raw, &cfg) != nil || cfg.Schema != 1 || !packageName.MatchString(cfg.Package) || !validNativeShare(cfg.Folder) || cfg.Folder.Picker != "folder" {
		return cfg, problem("SHARED_FOLDER_INVALID", "invalid native folder declaration")
	}
	return cfg, nil
}

// CheckPackage reuses the installer selection API for every package language.
// Compose additionally checks its materialized bind source; native paths are
// resolved afresh, so a moved share needs no generated absolute-path repair.
func CheckPackage(payload, data string) error {
	if _, err := os.Stat(filepath.Join(payload, "compose/compose.lock.json")); err == nil {
		return Check(filepath.Join(payload, "compose"), data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := NativePath(payload, data, "", "/")
	if errors.Is(err, os.ErrNotExist) { // Only an absent declaration means no opt-in.
		if _, e := os.Stat(filepath.Join(payload, "install/folder.json")); errors.Is(e, os.ErrNotExist) {
			return nil
		}
	}
	return err
}

// NativePath resolves the declared key through sharedfolder.Config. It never
// accepts an absolute folder from an App request or modifies permissions.
func NativePath(payload, data, key, root string) (string, error) {
	cfg, err := nativeConfig(payload)
	if err != nil {
		return "", err
	}
	if key != "" && key != cfg.Folder.Key {
		return "", sharedfolder.ErrNotConfigured
	}
	if data == "" {
		data = filepath.Join("/var/packages", cfg.Package, "var")
	}
	paths, err := sharedfolder.Subpaths(data)
	if err != nil {
		return "", err
	}
	if _, ok := paths[cfg.Folder.Key]; !ok {
		return "", problem("SHARED_FOLDER_INVALID", "folder selection missing; choose a folder in the upgrade wizard")
	}
	need := sharedfolder.Read
	if cfg.Folder.Permission == "rw" {
		need = sharedfolder.Write
	}
	path, err := (sharedfolder.Config{DataDir: data, Root: root, Account: cfg.Package, Subpaths: paths}).RequireAccess(cfg.Folder.Key, need)
	if err != nil {
		var permission *sharedfolder.PermissionError
		if errors.As(err, &permission) || errors.Is(err, sharedfolder.ErrIdentity) {
			return "", err
		}
		return "", &sharedfolder.SelectionError{Name: readShareName(data, cfg.Folder.Key), Subpath: paths[cfg.Folder.Key], Account: cfg.Package, Reason: "folder is missing, not a directory, or contains a symlink"}
	}
	return path, nil
}

// PrepareNative validates before DSM data-share acquisition. It checks syntax
// and directory structure only, deferring inaccessible descendants until grant.
// It writes no selection and creates no directory.
func PrepareNative(payload, data, pkg, root string) error {
	cfg, err := nativeConfig(payload)
	if err != nil {
		return err
	}
	if cfg.Package != pkg {
		return sharedfolder.ErrIdentity
	}
	if err = (sharedfolder.Config{Account: pkg}).ValidateIdentity(); err != nil {
		return err
	}
	f := cfg.Folder
	name, answered := os.LookupEnv(f.Key)
	subpath, present := os.LookupEnv(f.Key + "_subpath")
	if os.Getenv("SYNOPKG_PKG_STATUS") == "UPGRADE" && !present {
		data = pkgstate.Directory(pkg, data)
		names, e := sharedfolder.Names(data)
		if e != nil {
			return e
		}
		if answered && name != names[f.Key] {
			return problem("SHARED_FOLDER_INVALID", "a changed share requires an explicit relative folder path")
		}
		name = names[f.Key]
		paths, e := sharedfolder.Subpaths(data)
		if e != nil {
			return e
		}
		subpath, present = paths[f.Key]
	}
	if !present || !sharedfolder.ValidSubpath(subpath) {
		return problem("SHARED_FOLDER_INVALID", "select both the shared folder and relative folder path")
	}
	if err = sharedfolder.ValidateSelectionName(name); err != nil {
		return err
	}
	_, err = sharedfolder.ResolveSubpathForInstall(root, name, subpath)
	if errors.Is(err, sharedfolder.ErrNotFound) && f.Mode == "dedicated" && name == f.Name && subpath == "" && os.Getenv("SYNOPKG_PKGINST_TEMP_DIR") != "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("[SHARED_FOLDER_INVALID] selected folder is unavailable: %w", err)
	}
	return nil
}
