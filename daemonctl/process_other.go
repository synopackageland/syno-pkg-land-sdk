//go:build !linux

package main

import (
	"net"
	"syscall"
)

// Host-only tests exercise lifecycle/quoting; published controllers target Linux.
func childAttributes() *syscall.SysProcAttr     { return &syscall.SysProcAttr{} }
func processBirth(int) string                   { return "host-fixture" }
func recordedChildAlive(pid int, _ string) bool { return syscall.Kill(pid, 0) == nil }
func checkPeer(*net.UnixConn, int) error        { return nil }
