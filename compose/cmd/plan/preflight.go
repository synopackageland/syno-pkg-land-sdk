package main

import (
	"os"
	"path/filepath"

	"github.com/synopackageland/syno-pkg-land-sdk/compose"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/containerpreflight"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/sharepreflight"
)

func preflight() error {
	if err := containerpreflight.Check(); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	project := filepath.Dir(executable)
	return preflightProject(project, os.Getenv("SYNOPKG_PKGINST_TEMP_DIR"), os.Getenv("SYNOPKG_PKGVAR"))
}

func preflightProject(project, incoming, dataDir string) error {
	if err := preflightShares(project, incoming, dataDir); err != nil {
		return err
	}
	if incoming != "" && sameDirectory(project, filepath.Join(incoming, "compose")) {
		return nil
	}
	return compose.RefreshExternalOrigin(project, dataDir)
}

func preflightShares(project, incoming, dataDir string) error {
	// preinst runs the incoming tool before the account/config is installed.
	// Do not turn missing authorization into a failed install. The installed
	// tool checks again in prestart/start, including an INSTALL-marked start.
	if incoming != "" && sameDirectory(project, filepath.Join(incoming, "compose")) {
		return nil
	}
	return sharepreflight.Check(project, dataDir)
}

func sameDirectory(a, b string) bool {
	x, e := os.Stat(a)
	if e != nil {
		return false
	}
	y, e := os.Stat(b)
	return e == nil && os.SameFile(x, y)
}
