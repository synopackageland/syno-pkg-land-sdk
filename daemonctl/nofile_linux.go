//go:build linux

package main

import "syscall"

func applyNoFile(n int) error {
	if n == 0 {
		return nil
	}
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return err
	}
	soft, err := noFileSoftLimit(n, limit.Cur, limit.Max)
	if err != nil {
		return err
	}
	// Explicit Setrlimit also prevents Go exec from restoring its pre-runtime
	// soft limit in child processes. The inherited hard limit never changes.
	limit.Cur = soft
	return syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit)
}
