// Package datamigrate applies ordered, idempotent schema migrations to a
// package's data directory and records how far they have run.
//
// The data directory is the package's `var` folder (/var/packages/<package>/var,
// @appdata on the volume). DSM keeps it across upgrades and removes `tmp` and
// `target`, so schema changes belong here.
//
// Every run is a no-op once the recorded version covers all migrations; that
// check reads StateFile only and takes no lock, so each CGI request can call
// Run. A run with work to do takes an exclusive lock (LockFile, flock) first:
// across processes (CGI requests, the daemon, a lifecycle script) and
// goroutines one run applies the migrations while the others wait their turn,
// then read the recorded version again and find nothing left. The wait has no
// time limit, because a slow or swapping NAS only makes the holder slower;
// RunContext ends it on cancellation instead.
//
// A migration that fails stops the run with the previous version still
// recorded: the utility never deletes or rewrites data by itself, and the next
// run resumes from the last successful step. Migrations must still be
// idempotent: a crash between Apply and the version record re-runs one step.
package datamigrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// StateFile is the file, inside the data directory, that records progress.
const StateFile = "data-version"

// LockFile is the empty file, inside the data directory, whose flock lets
// one run at a time apply migrations. Keep it with the data directory.
const LockFile = ".data-version.lock"

// State is what StateFile holds.
type State struct {
	// Schema is the highest migration version applied so far; 0 means none.
	Schema int `json:"schema"`
	// PackageVersion is the package version recorded by the last run. It is the
	// fallback for Context.OldPkgVer when SYNOPKG_OLD_PKGVER is not set.
	PackageVersion string `json:"packageVersion,omitempty"`
}

// Context is the version context and directory a migration runs in.
type Context struct {
	// DataDir is the package's persistent var directory; DSM keeps it across
	// upgrades.
	DataDir string
	// OldPkgVer is the version being upgraded from: SYNOPKG_OLD_PKGVER when the
	// lifecycle environment provides it, otherwise the version the last
	// successful run recorded. Empty on a first install.
	OldPkgVer string
	// NewPkgVer is the version running now.
	NewPkgVer string
	// Logf, when set, receives one line per applied migration.
	Logf func(format string, args ...any)
}

// Migration is one numbered schema step. The app owns the meaning of Version;
// versions start at 1, never change meaning and never run twice once recorded.
type Migration struct {
	Version int
	Name    string
	Apply   func(Context) error
}

// Info is the version context of one run.
type Info struct {
	// OldPkgVer comes from SYNOPKG_OLD_PKGVER in a lifecycle script.
	OldPkgVer string
	// NewPkgVer is the package version running now.
	NewPkgVer string
	// Logf, when set, receives one line per applied migration.
	Logf func(format string, args ...any)
}

// Load reads the recorded state. A missing file is the zero state, not an
// error: that is a package that has never migrated.
func Load(dataDir string) (State, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, StateFile))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read %s: %w", StateFile, err)
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return State{}, fmt.Errorf("parse %s: %w", StateFile, err)
	}
	if state.Schema < 0 {
		return State{}, fmt.Errorf("%s: schema %d is negative", StateFile, state.Schema)
	}
	return state, nil
}

// Run validates the migrations, applies every one whose version is greater
// than the recorded schema version (lowest first) and records the package
// version. It returns the state after the run. On the first failure it stops
// and returns the error with the previous state intact.
//
// When another process or goroutine is applying migrations to the same data
// directory, Run waits for it, however long it takes, and then applies
// whatever is still left. Use RunContext to be able to stop waiting.
func Run(dataDir string, info Info, migrations []Migration) (State, error) {
	return RunContext(context.Background(), dataDir, info, migrations)
}

// RunContext is Run with a context that ends the wait for another run's lock,
// and stops the run before its next migration: a migration that has started
// finishes and is recorded. A run stopped this way returns ctx's error and the
// state recorded so far; the next run carries on from there.
func RunContext(ctx context.Context, dataDir string, info Info, migrations []Migration) (State, error) {
	if dataDir == "" {
		return State{}, errors.New("datamigrate: data directory is empty")
	}
	ordered, err := order(migrations)
	if err != nil {
		return State{}, err
	}
	current, err := Load(dataDir)
	if err != nil {
		return State{}, err
	}
	// Up to date: no lock. StateFile is replaced by rename, so it is read whole.
	if done(current, info, ordered) {
		return current, nil
	}
	if err := ctx.Err(); err != nil {
		return current, err
	}
	unlock, err := lock(ctx, dataDir)
	if err != nil {
		return current, err
	}
	defer unlock()
	// The run that held the lock may have applied everything meanwhile.
	if current, err = Load(dataDir); err != nil {
		return State{}, err
	}
	oldPkgVer := info.OldPkgVer
	if oldPkgVer == "" {
		oldPkgVer = current.PackageVersion
	}
	for _, migration := range ordered {
		if migration.Version <= current.Schema {
			continue
		}
		if err := ctx.Err(); err != nil {
			return current, err
		}
		step := Context{DataDir: dataDir, OldPkgVer: oldPkgVer, NewPkgVer: info.NewPkgVer, Logf: info.Logf}
		if err := migration.Apply(step); err != nil {
			return current, fmt.Errorf("schema migration %d (%s) failed: %w", migration.Version, migration.Name, err)
		}
		if info.Logf != nil {
			info.Logf("applied schema migration %d (%s)", migration.Version, migration.Name)
		}
		current.Schema = migration.Version
		if err := save(dataDir, current); err != nil {
			return current, err
		}
	}
	if info.NewPkgVer != "" && current.PackageVersion != info.NewPkgVer {
		current.PackageVersion = info.NewPkgVer
		if err := save(dataDir, current); err != nil {
			return current, err
		}
	}
	return current, nil
}

// done reports whether state already covers every migration and the package
// version, so a run would change nothing.
func done(state State, info Info, ordered []Migration) bool {
	if len(ordered) > 0 && ordered[len(ordered)-1].Version > state.Schema {
		return false
	}
	return info.NewPkgVer == "" || state.PackageVersion == info.NewPkgVer
}

// order checks the list before anything runs and returns a version-ascending
// copy, so a bad list never leaves half a migration behind.
func order(migrations []Migration) ([]Migration, error) {
	ordered := append([]Migration(nil), migrations...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Version < ordered[j].Version })
	seen := make(map[int]bool, len(ordered))
	for _, migration := range ordered {
		if migration.Version < 1 {
			return nil, fmt.Errorf("datamigrate: migration %q has version %d; versions start at 1", migration.Name, migration.Version)
		}
		if migration.Apply == nil {
			return nil, fmt.Errorf("datamigrate: migration %d (%s) has no Apply function", migration.Version, migration.Name)
		}
		if seen[migration.Version] {
			return nil, fmt.Errorf("datamigrate: duplicate migration version %d", migration.Version)
		}
		seen[migration.Version] = true
	}
	return ordered, nil
}

// save writes the state through a temporary file in the same directory and
// renames it, so a reader never sees a partial file.
func save(dataDir string, state State) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dataDir, "."+StateFile+"-*")
	if err != nil {
		return fmt.Errorf("create %s: %w", StateFile, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", StateFile, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", StateFile, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", StateFile, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dataDir, StateFile)); err != nil {
		return fmt.Errorf("replace %s: %w", StateFile, err)
	}
	return nil
}
