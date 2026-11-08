package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Only sysfs is read: no block-device open, SMART command, DSM API, persistent
// configuration, or filesystem write is needed to observe disk wakefulness.
// md0 contains the protected store; app volumes alone do not prove it awake.
func auditDisksAwake(sys string) bool {
	slaves := filepath.Join(sys, "class/block/md0/slaves")
	members, err := os.ReadDir(slaves)
	if err != nil || len(members) == 0 || len(members) > 256 {
		return false
	}
	for _, member := range members {
		disk, err := filepath.EvalSymlinks(filepath.Join(slaves, member.Name()))
		if err != nil {
			return false
		}
		if _, err := os.Stat(filepath.Join(disk, "partition")); err == nil {
			disk = filepath.Dir(disk)
		} else if !os.IsNotExist(err) {
			return false
		}
		if !auditDiskAwake(disk) {
			return false
		}
	}
	return true
}

func auditDiskAwake(disk string) bool {
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(disk, name))
		return strings.TrimSpace(string(b)), err
	}
	// A nonrotating member has no spinning disk to wake. Missing indicators on
	// any rotating/unknown member are never taken as lack of hibernation support.
	if rotational, err := read("queue/rotational"); err == nil && rotational == "0" {
		return true
	}
	if spin, err := read("device/syno_spindown"); err == nil {
		return spin == "0"
	}
	// DSM's counter is (jiffies - last_io) / HZ + 1, not a boolean or a
	// configured sleep timeout. Only fresh I/O proves awake without spindown.
	if idle, err := read("device/syno_idle_time"); err == nil {
		n, err := strconv.ParseUint(idle, 10, 64)
		return err == nil && n >= 1 && n <= 2
	}
	return false
}
