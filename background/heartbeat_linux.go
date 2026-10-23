package background

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"strconv"
	"strings"
)

func heartbeatBases() []string { return []string{"/tmp", "/run", "/dev/shm"} }
func memoryFilesystem(path string) error {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return err
	}
	if st.Type != unix.TMPFS_MAGIC {
		return errors.New("heartbeat requires tmpfs")
	}
	return nil
}

// Boot identity plus kernel start ticks rejects reboot and PID reuse. /proc
// ownership also binds the observed process to this package's account.
func processStamp(pid int) (string, error) {
	if pid <= 0 {
		return "", ErrInvalid
	}
	path := "/proc/" + strconv.Itoa(pid)
	info, err := os.Stat(path)
	if err != nil || !ownedByUs(info) {
		return "", ErrInvalid
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path + "/stat")
	if err != nil {
		return "", err
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", ErrInvalid
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return "", ErrInvalid
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19], nil
}
