package main

import "fmt"

func validateNoFile(n int) error {
	if n != 0 && (n < 1024 || n > 1048576) {
		return fmt.Errorf("daemon nofile must be omitted or between 1024 and 1048576")
	}
	return nil
}

// The package system unit supplies the hard ceiling. Never request a higher
// hard limit or privileges from inside the non-root daemon controller.
func noFileSoftLimit(n int, current, hard uint64) (uint64, error) {
	if err := validateNoFile(n); err != nil {
		return 0, err
	}
	if n == 0 {
		return current, nil
	}
	if hard < uint64(n) {
		return 0, fmt.Errorf("DAEMON_NOFILE_INSUFFICIENT: inherited hard limit %d is below requested %d; start through the acquired package system unit", hard, n)
	}
	return max(current, uint64(n)), nil
}
