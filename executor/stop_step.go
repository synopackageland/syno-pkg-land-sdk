package main

import (
	"context"
	"strings"
	"time"
)

// Legacy step bound; a policy with lifecycle budgets derives its step from
// the total stop allowance after job drain and response reserve.
const stopStepTimeout = 30 * time.Second

// rootStop is the App's own root step when the package stops or upgrades:
// the op the approved policy names as stop, consented to by that approval.
type rootStop struct {
	Approved func(context.Context) error
	Runner   RootRunner
	State    string
}

// run never stops the stop: reason upgrade is the step's prepare-upgrade.
func (r rootStop) run(ctx context.Context, p Policy, reason string, log func(string)) {
	arg := "stop"
	if reason == "upgrade" {
		arg = "prepare-upgrade"
	}
	runApprovedStep(ctx, r.Approved, r.Runner, p, p.Stop, "stop", []string{arg}, r.State, lifecycleStepBudget(p, "stop"), log)
}

// stopReason reads the stop answer the package account writes before its
// stop request: upgrade, or a plain stop.
func stopReason(path string) string {
	b, _, err := readLifecycleFile(path, 64)
	if err == nil && strings.TrimSuffix(string(b), "\n") == "upgrade" {
		return "upgrade"
	}
	return "stop"
}

// endRoot ends the root work of a closing service: its jobs, then for a
// package stop or upgrade the stop step.
func (e *Executor) endRoot(reason string) {
	e.rootMu.Lock()
	if e.shutdownUntil.IsZero() {
		e.shutdownUntil = time.Now().Add(e.Policy.budgets().Duration("stop"))
	}
	e.rootMu.Unlock()
	e.stopJobs(reason)
	if (reason == "stop" || reason == "upgrade") && e.StopStep != nil {
		e.StopStep(reason)
	}
}

func lifecycleStepBudget(p Policy, action string) time.Duration {
	if p.Timeouts == nil {
		switch action {
		case "start":
			return startStepTimeout
		case "stop":
			return stopStepTimeout
		case "purge":
			return purgeStepTimeout
		}
	}
	total := p.budgets().Duration(action)
	if action == "stop" {
		total -= total / 3
	}
	if action == "purge" {
		total = min(total/3, p.budgets().Duration("start"))
	}
	return max(0, total-min(5*time.Second, total/4))
}
