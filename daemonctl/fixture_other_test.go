//go:build !linux

package main

import "os/exec"

func dropFixtureUser(cmd *exec.Cmd) {}
