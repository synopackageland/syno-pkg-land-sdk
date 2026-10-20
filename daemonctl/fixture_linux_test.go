//go:build linux

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func dropFixtureUser(cmd *exec.Cmd) {
	if os.Geteuid() == 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{65534}}}
	}
}
