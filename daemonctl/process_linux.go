//go:build linux

package main

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
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

// A surviving group member keeps a lost supervisor cleanup-required even if
// its direct child died. Observation can refuse takeover; it never signals a
// numeric group from inventory, including a possibly reused group identity.
func recordedGroupAlive(group int) bool {
	if group <= 1 {
		return false
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		end := strings.LastIndex(string(raw), ")")
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(raw)[end+1:])
		if len(fields) > 2 && fields[2] == strconv.Itoa(group) && fields[0] != "Z" && fields[0] != "X" {
			return true
		}
	}
	return false
}
