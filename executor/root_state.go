package main

import (
	"errors"
	"path/filepath"
	"regexp"
)

// DSM keeps a package's var on a volume as /volumeN/@appdata/<package>.
var appDataRE = regexp.MustCompile(`^(/volume[0-9]+)/@appdata/([^/]+)$`)

// rootStateFor is the root state directory beside the package data dir
// (the resolved /var/packages/<package>/var): /volumeN/@synopkgland/<package>.
// Data anywhere else has none.
func rootStateFor(data, pkg string) (string, error) {
	m := appDataRE.FindStringSubmatch(data)
	if m == nil || m[2] != pkg || !tokenRE.MatchString(pkg) {
		return "", errors.New("package data is not on a volume")
	}
	return filepath.Join(m[1], "@synopkgland", pkg), nil
}

// ensureRootState makes the directory root:root 0700 below a root:root 0755
// @synopkgland, refusing symlinks and writable ancestors.
func ensureRootState(state string) error { return secureDir(state, 0700) }
