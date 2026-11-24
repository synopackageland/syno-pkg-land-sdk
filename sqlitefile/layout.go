// Package sqlitefile prepares directories for App-owned SQLite databases and
// provides driver-independent conversion and maintenance helpers.
package sqlitefile

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// NOCOWState describes the Btrfs NoCOW inode flag of a directory or file.
type NOCOWState string

const (
	StateOff         NOCOWState = "off"
	StateOn          NOCOWState = "on"
	StateUnsupported NOCOWState = "unsupported"
)

var warnPrepareOnce sync.Once

func optedOut() bool { return os.Getenv("SYNO_SQLITE_NOCOW") == "0" }

// PrepareDir creates dir with mode 0700 if needed. On Btrfs it enables NoCOW
// for newly created files unless dir has a compression policy or the App has
// opted out. Filesystem optimization failures are logged at most once per
// process. Only directory creation errors are returned.
func PrepareDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create SQLite directory: %w", err)
	}
	if !optedOut() {
		if err := prepareNoCOW(dir); err != nil {
			warnPrepareOnce.Do(func() {
				log.Printf("sqlitefile: prepare NoCOW directory: %v", err)
			})
		}
	}
	return nil
}

// State reports whether the path has the NoCOW inode flag. Inspect the
// directory for new-file inheritance and each existing file for its own flag.
// An unavailable path, unsupported filesystem or failed probe is unsupported.
func State(path string) NOCOWState {
	state, err := probeState(path)
	if err != nil {
		return StateUnsupported
	}
	return state
}
