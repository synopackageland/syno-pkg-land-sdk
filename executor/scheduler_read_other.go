//go:build !linux

package main

func runSchedulerRead([]string) error { return errLinux }
