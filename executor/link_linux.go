//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func linkOpenPart(part, dir *os.File, name string) error {
	// /proc/self/fd/<fd> refers to the still-open inode. Following that one trusted
	// proc link prevents a subject from swapping a guessed temporary pathname.
	old, err := syscall.BytePtrFromString(fmt.Sprintf("/proc/self/fd/%d", part.Fd()))
	if err != nil {
		return err
	}
	new, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(syscall.SYS_LINKAT, ^uintptr(99), uintptr(unsafe.Pointer(old)), dir.Fd(), uintptr(unsafe.Pointer(new)), 0x400, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
