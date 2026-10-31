//go:build !linux

package supervision

import (
	"os/exec"
	"syscall"
)

func attributes(cmd *exec.Cmd)                          {}
func signalGroup(cmd *exec.Cmd, s syscall.Signal) error { return cmd.Process.Signal(s) }
func (p *Process) reap() {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.err = err
	p.reaped = true
	close(p.done)
	p.mu.Unlock()
}
