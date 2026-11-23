//go:build !linux

package main

// The package supervisor runs on Linux. On local test hosts nice may be
// process-wide, so do not change the scheduling priority of the test binary.
func lowPriorityCompression() {}
