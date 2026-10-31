// Package supervision supplies the restart decision shared by package daemons
// and their provider children. Policy owns no goroutines, timers or processes.
// Its caller serializes events, cleans each generation before restarting, and
// discards timers when NextRetry is zero. A failed policy requires a real trigger.
package supervision

import (
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type State string

const (
	Starting State = "starting"
	Waiting  State = "waiting"
	Running  State = "running"
	Backoff  State = "backoff"
	Failed   State = "failed"
	Stopped  State = "stopped"
)

type Config struct {
	Initial, Max, Min, Stable, Window time.Duration
	Burst                             int
}

func Defaults() Config {
	return Config{Initial: 2 * time.Second, Max: 5 * time.Minute, Min: time.Second, Stable: time.Minute, Window: 10 * time.Minute, Burst: 8}
}
func (c Config) Normalize() Config {
	d := Defaults()
	if c.Initial <= 0 {
		c.Initial = d.Initial
	}
	if c.Max <= 0 {
		c.Max = d.Max
	}
	if c.Min <= 0 {
		c.Min = min(d.Min, c.Initial)
	}
	if c.Stable <= 0 {
		c.Stable = d.Stable
	}
	if c.Window <= 0 {
		c.Window = d.Window
	}
	if c.Burst <= 0 {
		c.Burst = d.Burst
	}
	c.Max = max(c.Initial, c.Max)
	c.Min = min(c.Min, c.Initial)
	return c
}

type Exit struct {
	Code      int    `json:"code"`
	Reason    string `json:"reason,omitempty"`
	Permanent bool   `json:"permanent,omitempty"`
}

// ExitOf retains bounded diagnostics, never raw child output or command argv.
func ExitOf(err error) Exit {
	if err == nil {
		return Exit{Code: 0, Reason: "child_exit"}
	}
	var e *exec.ExitError
	if errors.As(err, &e) {
		return Exit{Code: e.ExitCode(), Reason: "child_exit"}
	}
	return Exit{Code: -1, Reason: "start_failed", Permanent: PermanentStart(err)}
}
func PermanentStart(err error) bool {
	if errors.Is(err, ErrCleanupRequired) {
		return true
	}
	return errors.Is(err, syscall.ENOEXEC) || errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || errors.Is(err, exec.ErrNotFound)
}

type Snapshot struct {
	State     State     `json:"state"`
	Attempts  int       `json:"attempts"`
	NextRetry time.Time `json:"nextRetry,omitempty"`
	LastExit  *Exit     `json:"lastExit,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Log       string    `json:"log,omitempty"`
}

type Policy struct {
	mu       sync.Mutex
	cfg      Config
	random   func(time.Duration) time.Duration
	failures []time.Time
	started  time.Time
	ceiling  time.Duration
	snapshot Snapshot
}

func New(c Config, random func(time.Duration) time.Duration) *Policy {
	c = c.Normalize()
	if random == nil {
		random = func(n time.Duration) time.Duration {
			if n <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(n)))
		}
	}
	return &Policy{cfg: c, random: random, snapshot: Snapshot{State: Starting}}
}
func (p *Policy) Snapshot() Snapshot { p.mu.Lock(); defer p.mu.Unlock(); return p.snapshotCopy() }
func (p *Policy) snapshotCopy() Snapshot {
	s := p.snapshot
	if s.LastExit != nil {
		v := *s.LastExit
		s.LastExit = &v
	}
	return s
}

// Trigger starts one bounded series. Call only on user/config/package action or
// an actual dependency change, never because a poll or a window timer fired.
func (p *Policy) Trigger() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failures = nil
	p.started = time.Time{}
	p.ceiling = 0
	p.snapshot = Snapshot{State: Starting}
}
func (p *Policy) Started(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.snapshot.State == Failed {
		return
	}
	p.started = now
	p.snapshot.State = Starting
	p.snapshot.NextRetry = time.Time{}
}
func (p *Policy) Running() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.snapshot.State != Failed {
		p.snapshot.State = Running
		p.snapshot.Reason = ""
	}
}
func (p *Policy) Waiting(reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.snapshot.State != Failed {
		p.snapshot.State = Waiting
		p.snapshot.Reason = reason
		p.snapshot.NextRetry = time.Time{}
	}
}
func (p *Policy) Exited(now time.Time, exit Exit) Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.snapshot.State == Failed || p.snapshot.State == Stopped {
		return p.snapshotCopy()
	}
	if !p.started.IsZero() && now.Sub(p.started) >= max(p.cfg.Stable, 2*p.ceiling) {
		p.failures = nil
		p.ceiling = 0
	}
	p.started = time.Time{}
	p.snapshot.LastExit = &exit
	p.snapshot.NextRetry = time.Time{}
	p.snapshot.Reason = exit.Reason
	if exit.Permanent {
		p.snapshot.State = Failed
		return p.snapshotCopy()
	}
	if exit.Code == 0 {
		p.snapshot.State = Stopped
		return p.snapshotCopy()
	}
	cutoff := now.Add(-p.cfg.Window)
	for len(p.failures) > 0 && !p.failures[0].After(cutoff) {
		p.failures = p.failures[1:]
	}
	p.failures = append(p.failures, now)
	p.snapshot.Attempts = len(p.failures)
	if len(p.failures) >= p.cfg.Burst {
		p.snapshot.State = Failed
		p.snapshot.Reason = "restart_limit"
		return p.snapshotCopy()
	}
	if p.ceiling == 0 {
		p.ceiling = p.cfg.Initial
	} else {
		p.ceiling = min(p.ceiling*2, p.cfg.Max)
	}
	// Full jitter with a minimum spacing: Uniform[Min, ceiling]. The floor
	// prevents zero-delay loops; unlike equal jitter the whole usable range varies.
	width := p.ceiling - p.cfg.Min
	delay := p.cfg.Min
	if width > 0 {
		delay += max(0, min(p.random(width+1), width))
	}
	p.snapshot.State = Backoff
	p.snapshot.NextRetry = now.Add(delay)
	return p.snapshotCopy()
}
