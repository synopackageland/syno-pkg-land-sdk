package sharepreflight

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
)

// InstallSharedFolder reads the existing Compose lock declaration without
// importing the build-time Compose planner into the runtime gateway.
type InstallSharedFolder struct {
	Picker     string `json:"picker,omitempty"`
	Mode       string `json:"mode"`
	Name       string `json:"name,omitempty"`
	Key        string `json:"key"`
	Permission string `json:"permission"`
}

var packageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{1,63}$`)
var dedicatedShareName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
var systemShareName = regexp.MustCompile(`(?i)^(homes?|users|ftp|root|admin|guest|system|volume[0-9]*|(usb|sata|sd)share[0-9]*)$`)
var shareKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func validNativeShare(f *InstallSharedFolder) bool {
	return f != nil && (f.Picker == "" || f.Picker == "folder" && (f.Mode == "select" || f.Mode == "dedicated")) && (f.Mode == "select" && f.Name == "" || f.Mode == "dedicated" && dedicatedShareName.MatchString(f.Name) && !systemShareName.MatchString(f.Name)) && shareKey.MatchString(f.Key) && !strings.HasPrefix(f.Key, "pkgwizard_") && !strings.HasPrefix(f.Key, "synopkg") && !strings.HasPrefix(f.Key, "change_") && (f.Permission == "ro" || f.Permission == "rw")
}
func problem(code, message string) error { return fmt.Errorf("[%s] %s", code, message) }

// Check measures the installed selection as the matching package account.
// Each explicit start/retry rereads the lock and saved names, ignores wizard
// variables, and never grants access. Empty dataDir uses DSM's package var.
func Check(project, dataDir string) error {
	return check(project, dataDir, "/")
}

func check(project, dataDir, root string) error {
	f, err := os.Open(filepath.Join(project, "compose.lock.json"))
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	var lock struct {
		Package             string               `json:"package"`
		InstallSharedFolder *InstallSharedFolder `json:"install_shared_folder"`
		MountsBlocked       bool                 `json:"mounts_blocked"`
	}
	if err != nil || len(raw) > 4<<20 || json.Unmarshal(raw, &lock) != nil {
		return problem("COMPOSE_LOCK_INVALID", "cannot read shared folder declaration")
	}
	if lock.InstallSharedFolder == nil {
		return nil
	}
	if !validNativeShare(lock.InstallSharedFolder) || lock.MountsBlocked {
		return problem("COMPOSE_SHARE_INVALID", "invalid selected share declaration")
	}
	if dataDir == "" {
		if !packageName.MatchString(lock.Package) {
			return sharedfolder.ErrIdentity
		}
		dataDir = filepath.Join("/var/packages", lock.Package, "var")
	}
	need := sharedfolder.Read
	if lock.InstallSharedFolder.Permission == "rw" {
		need = sharedfolder.Write
	}
	cfg := sharedfolder.Config{DataDir: dataDir, Root: root, Account: lock.Package}
	if lock.InstallSharedFolder.Picker == "folder" {
		paths, e := sharedfolder.Subpaths(dataDir)
		if e != nil {
			return e
		}
		if _, ok := paths[lock.InstallSharedFolder.Key]; !ok {
			return problem("COMPOSE_SHARE_INVALID", "folder selection missing; choose a folder in the upgrade wizard")
		}
		cfg.Subpaths = paths
	}
	if lock.Package == "" {
		return sharedfolder.ErrIdentity
	}
	selectedPath, err := cfg.RequireAccess(lock.InstallSharedFolder.Key, need)
	if err != nil {
		if lock.InstallSharedFolder.Picker == "folder" {
			var permission *sharedfolder.PermissionError
			if errors.As(err, &permission) {
				return err
			}
			reason := "folder is missing, not a directory, or contains a symlink"
			return &sharedfolder.SelectionError{Name: readShareName(dataDir, lock.InstallSharedFolder.Key), Subpath: cfg.Subpaths[lock.InstallSharedFolder.Key], Account: lock.Package, Reason: reason}
		}
		return fmt.Errorf("shared folder preflight: %w", err)
	}
	if lock.InstallSharedFolder.Picker == "folder" {
		env, e := os.ReadFile(filepath.Join(project, ".env"))
		if e != nil {
			return e
		}
		expected := "SYNOPKGLAND_MOUNT_" + lock.InstallSharedFolder.Key + "='" + strings.ReplaceAll(selectedPath, "'", `\'`) + "'"
		found := false
		for _, line := range strings.Split(string(env), "\n") {
			if line == expected {
				found = true
			}
		}
		if !found {
			return &sharedfolder.SelectionError{Name: readShareName(dataDir, lock.InstallSharedFolder.Key), Subpath: cfg.Subpaths[lock.InstallSharedFolder.Key], Account: lock.Package, Reason: "folder location changed; repair or upgrade to regenerate its container bind mount"}
		}
	}
	return nil
}

func readShareName(dataDir, key string) string {
	names, _ := sharedfolder.Names(dataDir)
	return names[key]
}
