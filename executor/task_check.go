package main

import (
	"context"
	"sync"
	"time"
)

// The running executor reads its approval tasks back every taskCheckEvery
// after a pass and every taskCheckRetry after a failure; a readback that
// takes longer than taskCheckSlow fails. A pass stays ready in status for
// one period and two slow readbacks, so a run of passes never reads stale.
const (
	taskCheckEvery = 10 * time.Minute
	taskCheckRetry = time.Minute
	taskCheckSlow  = 150 * time.Second
	taskCheckFresh = taskCheckEvery + 2*taskCheckSlow
)

// taskCheck is the executor's own readback of its approval tasks
// (spec/scheduler/v1), which status reports as tasks so that a DSM window
// need not read the scheduler before it shows the App.
type taskCheck struct {
	Check func(context.Context) error
	Now   func() time.Time
	// Base bounds a readback that a stale status starts.
	Base    context.Context
	mu      sync.Mutex
	ended   bool
	passed  bool
	at      time.Time
	running bool
}

// run reads the tasks back once, unless a readback is under way. The time a
// readback started is when it checked: the tasks may change while it runs.
func (c *taskCheck) run(ctx context.Context) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()
	started := c.Now()
	ctx, cancel := context.WithTimeout(ctx, taskCheckSlow)
	err := c.Check(ctx)
	cancel()
	c.mu.Lock()
	c.ended, c.passed, c.at, c.running = true, err == nil, started, false
	c.mu.Unlock()
}

// next is how long the loop waits after the readback that just ended.
func (c *taskCheck) next() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.passed {
		return taskCheckEvery
	}
	return taskCheckRetry
}

// loop reads the tasks back until ctx ends; the first readback is the
// caller's.
func (c *taskCheck) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.next()):
		}
		c.run(ctx)
	}
}

// Status is tasks as status answers it: state checking until the first
// readback ends, then ready, stale (a pass older than taskCheckFresh) or
// failed, with checked_at. A stale result starts a readback: the loop's
// timer may have slept through a suspended NAS.
func (c *taskCheck) Status() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ended {
		return map[string]any{"state": "checking"}
	}
	state := "failed"
	if c.passed {
		state = "ready"
		if c.Now().Sub(c.at) > taskCheckFresh {
			state = "stale"
			if !c.running && c.Base != nil {
				go c.run(c.Base)
			}
		}
	}
	return map[string]any{"state": state, "checked_at": c.at.UTC().Format(time.RFC3339)}
}
