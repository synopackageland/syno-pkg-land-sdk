package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/sharedfolder"
)

// InstallSharedFolder is the existing native install wizard declaration. It
// selects a name, never a host path or an ACL grant. DSM's administrator owns
// permission changes; the container still runs as the fixed package account.
type InstallSharedFolder struct {
	Mode       string `json:"mode"`
	Name       string `json:"name,omitempty"`
	Key        string `json:"key"`
	Permission string `json:"permission"`
}

var dedicatedShareName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
var systemShareName = regexp.MustCompile(`(?i)^(homes?|users|ftp|root|admin|guest|system|volume[0-9]*|(usb|sata|sd)share[0-9]*)$`)

var nativeShareKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func validNativeShare(f *InstallSharedFolder) bool {
	return f != nil && (f.Mode == "select" && f.Name == "" || f.Mode == "dedicated" && dedicatedShareName.MatchString(f.Name) && !systemShareName.MatchString(f.Name)) && nativeShareKey.MatchString(f.Key) &&
		!strings.HasPrefix(f.Key, "pkgwizard_") && !strings.HasPrefix(f.Key, "synopkg") && !strings.HasPrefix(f.Key, "change_") &&
		(f.Permission == "ro" || f.Permission == "rw")
}

func nativeShares(r Request) (*InstallSharedFolder, error) {
	var native *InstallSharedFolder
	for key, s := range r.Shares {
		if s.Select != "install" {
			continue
		}
		if !validNativeShare(r.InstallSharedFolder) || key != r.InstallSharedFolder.Key || s.Permission != r.InstallSharedFolder.Permission {
			return nil, problem("COMPOSE_SHARE_INVALID", "install share must match install.sharedFolder mode: select or dedicated, key and permission")
		}
		f := *r.InstallSharedFolder
		native = &f
	}
	if native != nil && len(r.Shares) != 1 {
		return nil, problem("COMPOSE_SHARE_INVALID", "native install selection cannot mix with dynamic mount authority")
	}
	return native, nil
}

// nativeShareEnvironment materializes a DSM share name only, never a path from
// application requests. An upgrade's unchecked CHANGE_key retains the saved
// selection even when DSM prefills another share in the wizard.
func nativeShareEnvironment(f *InstallSharedFolder, dataDir, root string) (string, error) {
	if !validNativeShare(f) {
		return "", problem("COMPOSE_SHARE_INVALID", "invalid native install selection")
	}
	name := ""
	change := os.Getenv("CHANGE_" + f.Key)
	switch change {
	case "true", "1", "yes", "on":
	default:
		change = ""
	}
	_, answered := os.LookupEnv(f.Key)
	if os.Getenv("SYNOPKG_PKG_STATUS") == "INSTALL" || (os.Getenv("SYNOPKG_PKG_STATUS") == "UPGRADE" && (change != "" || f.Mode == "dedicated" && answered)) {
		name = os.Getenv(f.Key)
		if name == "" && f.Mode == "dedicated" {
			name = f.Name
		}
	} else {
		names, err := sharedfolder.Names(dataDir)
		if err != nil {
			return "", problem("COMPOSE_SHARE_INVALID", "cannot read installed share selection")
		}
		name = names[f.Key]
		if name == "" && f.Mode == "dedicated" {
			name = f.Name
		}
	}
	// Keep the native installer contract: no control characters or dotenv lines.
	for _, c := range name {
		if c < 32 || c == 127 {
			return "", problem("COMPOSE_SHARE_INVALID", "invalid share name")
		}
	}
	source, err := sharedfolder.Resolve(root, name)
	if errors.Is(err, sharedfolder.ErrNotFound) && f.Mode == "dedicated" && name == f.Name && os.Getenv("SYNOPKG_PKGINST_TEMP_DIR") != "" {
		// DSM acquires data-share after preinst. Its new share is on the selected
		// package volume; never create or chmod that directory as the package user.
		volume := os.Getenv("SYNOPKG_PKGDEST_VOL")
		if !regexp.MustCompile(`^/volume[0-9]+$`).MatchString(volume) {
			return "", problem("COMPOSE_SHARE_INVALID", "DSM installation volume is required for the dedicated shared folder")
		}
		return "SYNOPKGLAND_MOUNT_" + f.Key + "=" + dotenvLiteral(filepath.Join(volume, name)) + "\n", nil
	}
	if err != nil {
		return "", fmt.Errorf("[COMPOSE_SHARE_INVALID] selected shared folder %q is unavailable: %w", name, err)
	}
	// The administrator owns the volume/share directory entries. Never allow a
	// symlink to turn a selected share name into an arbitrary host bind source.
	for current := source; current != filepath.Clean(root) && current != "/" && current != "."; current = filepath.Dir(current) {
		st, err := os.Lstat(current)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", problem("COMPOSE_SHARE_INVALID", "selected share must have no symlink components")
		}
	}
	return "SYNOPKGLAND_MOUNT_" + f.Key + "=" + dotenvLiteral(source) + "\n", nil
}

func dotenvLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", `\'`) + "'" }
