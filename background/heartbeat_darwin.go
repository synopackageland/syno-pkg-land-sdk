package background

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

// macOS is a development host; DSM/Linux always requires a verified tmpfs.
func heartbeatBases() []string      { return []string{os.TempDir()} }
func memoryFilesystem(string) error { return nil }
func processStamp(pid int) (string, error) {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if p == nil || int(p.Eproc.Ucred.Uid) != os.Getuid() || p.Proc.P_stat == 5 {
		return "", ErrInvalid
	}
	return fmt.Sprintf("%d:%d", p.Proc.P_starttime.Sec, p.Proc.P_starttime.Usec), nil
}
