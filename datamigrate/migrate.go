// Package datamigrate applies ordered, idempotent schema migrations to a
// package's data directory and records how far they have run.
//
// The data directory is the package's `var` folder (/var/packages/<package>/var,
// @appdata on the volume). DSM keeps it across upgrades and removes `tmp` and
// `target`, so schema changes belong here.
//
// Every run is a no-op once the recorded version covers all migrations. A
// migration that fails stops the run with the previous version still recorded:
// the utility never deletes or rewrites data by itself, and the next run
// resumes from the last successful step. Migrations must therefore be
// idempotent — a crash between Apply and the version record re-runs one step,
// and two concurrent CGI requests can both reach a step the first time.
package datamigrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// StateFile is the file, inside the data directory, that records progress.
const StateFile = "data-version"

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
func Run(dataDir string, info Info, migrations []Migration) (State, error) {
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
	oldPkgVer := info.OldPkgVer
	if oldPkgVer == "" {
		oldPkgVer = current.PackageVersion
	}
	for _, migration := range ordered {
		if migration.Version <= current.Schema {
			continue
		}
		ctx := Context{DataDir: dataDir, OldPkgVer: oldPkgVer, NewPkgVer: info.NewPkgVer, Logf: info.Logf}
		if err := migration.Apply(ctx); err != nil {
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
