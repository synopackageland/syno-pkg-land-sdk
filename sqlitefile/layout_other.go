//go:build !linux

package sqlitefile

import "os"

func prepareNoCOW(string) error             { return nil }
func probeState(string) (NOCOWState, error) { return StateUnsupported, nil }
func eligibleDir(string) (bool, error)      { return false, nil }

// The conversion path is unreachable without an eligible Btrfs directory.
func availableBytes(string) (uint64, error) { return 0, nil }
func hasSingleLink(os.FileInfo) bool        { return false }
func checkReplacementMetadata(*os.File, *os.File, os.FileInfo, os.FileInfo) error {
	return nil
}
