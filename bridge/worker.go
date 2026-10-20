//go:build unix

package bridge

import (
	"context"
	"errors"
	"fmt"

	"github.com/synopackageland/syno-pkg-land-sdk/background"
)

// ErrNoBackground means bridge.json enables neither jobs nor delegate, so the
// package has no jobs for a worker to run.
var ErrNoBackground = errors.New("bridge: bridge.json enables no background capability (jobs, delegate)")

// Worker runs the package's background jobs until ctx ends: the jobs bg.*
// and delegate.* queued in the data directory, as delegate.Service.Run runs
// them for a Go App. It never runs as root and starts no App: v1 jobs are
// the SDK's own workers (sha256-v1, download-to-folder-v1).
func Worker(ctx context.Context, cfg Config, info background.WorkerInfo) error {
	if err := background.RefuseRoot(info.UID); err != nil {
		return fmt.Errorf("bridge: the worker never runs as root: %w", err)
	}
	if !packageID.MatchString(cfg.PackageID) {
		return fmt.Errorf("bridge: invalid package ID %q", cfg.PackageID)
	}
	cfg = cfg.withDefaults()
	m, err := cfg.manifest()
	if err != nil {
		return err
	}
	if !m.Capabilities.Jobs && m.Capabilities.Delegate == nil {
		return ErrNoBackground
	}
	return cfg.delegate(m.Capabilities.Delegate, background.NewStore(cfg.jobDir())).Run(ctx, info)
}
