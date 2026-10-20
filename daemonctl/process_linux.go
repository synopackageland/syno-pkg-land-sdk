//go:build linux

package main

import (
	"errors"
	"net"
	"os"
	"strconv"
	"syscall"
)

func childAttributes() *syscall.SysProcAttr { return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} }
func processBirth(pid int) string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	birth, _ := parseBirth(string(raw))
	return birth
}
func recordedChildAlive(pid int, birth string) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return true
	}
	actual, alive := parseBirth(string(raw))
	if actual == "" {
		return true
	}
	return alive && (birth == "" || actual == birth)
}
func checkPeer(conn *net.UnixConn, pid int) error {
	fd, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var peer *syscall.Ucred
	var getErr error
	if err := fd.Control(func(raw uintptr) {
		peer, getErr = syscall.GetsockoptUcred(int(raw), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if getErr != nil {
		return getErr
	}
	if peer.Uid != uint32(os.Geteuid()) || peer.Pid != int32(pid) {
		return errors.New("foreign daemon supervisor")
	}
	return nil
}
