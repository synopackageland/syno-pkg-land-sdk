package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

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
	if err := preflightProject(project, incoming, os.Getenv("SYNOPKG_PKGVAR")); err != nil {
		return err
	}
	// INSTALL/UPGRADE workers generate profiles themselves. Waiting before that
	// worker would deadlock a clean install. A subsequent explicit START uses
	// the existing project and requires its profiles to have caught up first.
	if !workerProfileWaitRequired(project, incoming, os.Getenv("SYNOPKG_PKG_STATUS")) {
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	return compose.WaitForWorkerProfiles(ctx, project, func() {
		fmt.Fprintln(os.Stderr, "[COMPOSE_WORKER_PROFILE_PENDING] Waiting for Container Manager to register this App; keep this operation running. If registration does not recover, cancel and repair this App in Package Center")
	})
}

func workerProfileWaitRequired(project, incoming, status string) bool {
	return status == "START" && (incoming == "" || !sameDirectory(project, filepath.Join(incoming, "compose")))
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
