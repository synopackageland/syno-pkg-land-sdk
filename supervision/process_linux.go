//go:build linux

package supervision

import (
	"errors"
	"golang.org/x/sys/unix"
	"os/exec"
	"syscall"
)

func attributes(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
func signalGroup(cmd *exec.Cmd, s syscall.Signal) error { return syscall.Kill(-cmd.Process.Pid, s) }
func (p *Process) reap() {
	// Observe exit without reaping: the zombie pins its PID/PGID until every
	// surviving descendant has been signalled. Stop and reap share the mutex.
	var info unix.Siginfo
	var err error
	for {
		err = unix.Waitid(unix.P_PID, p.PID(), &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	p.mu.Lock()
	if err == nil {
		_, err = GroupExited(p.PID())
	}
	p.err = p.cmd.Wait()
	if err != nil {
		p.err = errors.Join(ErrCleanupRequired, err, p.err)
	}
	p.reaped = true
	close(p.done)
	p.mu.Unlock()
}

// GroupExited observes only a direct, unreaped child and cleans its owned
// process group before its caller reaps. It supports a serialized privileged
// adapter with an existing authentication contract. Never call it on inventory.
// ECHILD refuses cleanup; the API does not infer authority from a numeric PID.
func GroupExited(pid int) (bool, error) {
	var info unix.Siginfo
	err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT|unix.WNOHANG, nil)
	if errors.Is(err, unix.EINTR) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Signo == 0 {
		return false, nil
	}
	// The zombie still pins the group ID until the caller's serialized reap.
	err = syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		err = nil
	}
	return true, err
}
