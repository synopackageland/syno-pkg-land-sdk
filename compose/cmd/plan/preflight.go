package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/synopackageland/syno-pkg-land-sdk/compose"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/containerpreflight"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/sharepreflight"
)

func preflight() error {
	return preflightWith(containerpreflight.Check, os.Executable)
}

func preflightWith(provider func() error, executablePath func() (string, error)) error {
	if err := provider(); err != nil {
		return err
	}
	executable, err := executablePath()
	if err != nil {
		return err
	}
	project := filepath.Dir(executable)
	incoming := os.Getenv("SYNOPKG_PKGINST_TEMP_DIR")
	if incoming != "" && sameDirectory(project, filepath.Join(incoming, "compose")) {
		if hint := compose.ProjectRecoveryHint(project); hint != "" {
			fmt.Fprintln(os.Stderr, hint)
			// DSM displays this hook log when its later worker acquisition fails.
			if path := os.Getenv("SYNOPKG_TEMP_LOGFILE"); path != "" {
				if f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0600); err == nil {
					fmt.Fprintln(f, hint)
					f.Close()
				}
			}
		}
	}
	// Container Manager can remove profiles on STOP and recreate them in the
	// START worker. Waiting for those profiles here prevents that worker running.
	return preflightProject(project, incoming, os.Getenv("SYNOPKG_PKGVAR"))
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
