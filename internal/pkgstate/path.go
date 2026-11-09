// Package pkgstate resolves DSM-owned package data during installation.
package pkgstate

import (
	"os"
	"path/filepath"
)

// Directory also works before DSM recreates the package var symlink on upgrade.
// Explicit paths and DSM's physical package-var path take precedence.
func Directory(pkg, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if value := os.Getenv("SYNOPKG_PKGVAR"); value != "" {
		return value
	}
	if volume := os.Getenv("SYNOPKG_PKGDEST_VOL"); volume != "" {
		return filepath.Join(volume, "@appdata", pkg)
	}
	return filepath.Join("/var/packages", pkg, "var")
}
