package supervision

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
)

var ErrCleanupRequired = errors.New("process group cleanup required")

// Process owns exactly one foreground process group. It never accepts a PID
// from inventory. The creating thread lives until reap (Linux PDEATHSIG).
type Process struct {
	cmd    *exec.Cmd
	done   chan struct{}
	mu     sync.Mutex
	reaped bool
	err    error
}

func Start(cmd *exec.Cmd) (*Process, error) {
	attributes(cmd)
	p := &Process{cmd: cmd, done: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		started <- nil
		p.reap()
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return p, nil
}
func (p *Process) Done() <-chan struct{} { return p.done }
func (p *Process) Alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
func (p *Process) PID() int    { return p.cmd.Process.Pid }
func (p *Process) Wait() error { <-p.done; return p.err }
func (p *Process) Exit() Exit  { <-p.done; return ExitOf(p.err) }
func (p *Process) signal(sig syscall.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reaped {
		return nil
	}
	err := signalGroup(p.cmd, sig)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
func (p *Process) Stop(grace time.Duration) error {
	if !p.Alive() {
		if errors.Is(p.err, ErrCleanupRequired) {
			return ErrCleanupRequired
		}
		return nil
	}
	if err := p.signal(syscall.SIGTERM); err != nil {
		return err
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-p.done:
		if errors.Is(p.err, ErrCleanupRequired) {
			return ErrCleanupRequired
		}
		return nil
	case <-timer.C:
	}
	if err := p.signal(syscall.SIGKILL); err != nil {
		return err
	}
	timer.Reset(2 * time.Second)
	select {
	case <-p.done:
		if errors.Is(p.err, ErrCleanupRequired) {
			return ErrCleanupRequired
		}
		return nil
	case <-timer.C:
		return errors.New("process group cleanup required")
	}
}
