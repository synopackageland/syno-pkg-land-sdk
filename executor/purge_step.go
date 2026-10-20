package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/executor/internal/taskscript"
)

const (
	purgeStepTimeout   = 60 * time.Second
	purgeStepLogOutput = 4096
)

// rootPurge is the App's own root step at uninstall: the op the approved
// policy names as purge. No admin session exists then, so the live approval
// is the consent. Approved is nil when nothing can prove it, as for an
// offline purge.
type rootPurge struct {
	Approved    func(context.Context) error
	Runner      RootRunner
	DeleteState bool
	// State is the root state directory, removed after the step for
	// delete-state.
	State string
}

// run never stops the purge: a broken App step must not trap the uninstall.
func (r rootPurge) run(ctx context.Context, p Policy, log func(string)) {
	arg := "keep-state"
	if r.DeleteState {
		arg = "delete-state"
	}
	runApprovedStep(ctx, r.Approved, r.Runner, p, p.Purge, "purge", []string{arg}, r.State, lifecycleStepBudget(p, "purge"), log)
	if r.DeleteState && r.State != "" {
		if err := os.RemoveAll(r.State); err != nil {
			log("root state removal failed: package=" + p.Package + ": " + err.Error())
			return
		}
		log("root state removed: package=" + p.Package)
	}
}

// runApprovedStep runs the lifecycle step name, if the policy names one,
// while the approval is live, and logs how it ended as "<mode> root step".
// It answers how the step ended and its output; exit is empty when the step
// did not run.
func runApprovedStep(ctx context.Context, approved func(context.Context) error, runner RootRunner, p Policy, name, mode string, args []string, state string, timeout time.Duration, log func(string)) (exit, out string) {
	if name == "" {
		return "", ""
	}
	label := mode + " root step"
	if approved == nil || runner == nil || approved(ctx) != nil {
		log(label + " skipped: not approved: package=" + p.Package)
		return "", ""
	}
	exit, out = runStep(ctx, runner, p, name, mode, args, state, timeout)
	if exit != "0" {
		log(label + " failed: package=" + p.Package + " exit=" + exit + " output=" + strconv.Quote(out))
		return exit, out
	}
	log(label + ": package=" + p.Package + " exit=0 output=" + strconv.Quote(out))
	return exit, out
}

// runStep runs the lifecycle step name of p with its arguments, and answers
// how it ended (an exit code, timeout, signal or refused) and up to 4 KiB of
// its output.
func runStep(ctx context.Context, runner RootRunner, p Policy, name, mode string, args []string, state string, timeout time.Duration) (string, string) {
	out := &headBuffer{limit: purgeStepLogOutput + 1}
	call := rootCall{Name: name, Entry: p.Root[name], Mode: mode, Args: args, Stdin: []byte("{}"), Env: stepEnv(p.Package, mode, state), Stdout: out, Stderr: out}
	code, err := runRoot(ctx, runner, call, timeout, nil)
	text := out.buf.String()
	if len(text) > purgeStepLogOutput {
		text = text[:purgeStepLogOutput] + "…"
	}
	var exit *exec.ExitError
	switch {
	case errors.Is(err, errRootTimeout), errors.Is(err, context.DeadlineExceeded):
		return "timeout", text
	case err == nil:
		return strconv.Itoa(code), text
	case errors.As(err, &exit):
		return "signal", text
	}
	return "refused", text
}

// liveTasks proves the approval of the policy pinned as pd still stands: both
// of its tasks are in the scheduler with exactly the canonical scripts. It
// runs right after the startup snapshot, whose list found the control task:
// a get of that id checks it again, and duplicates were refused by that list.
func liveTasks(ctx context.Context, d *liveDSM, pkg, pd string, inv taskInventory) error {
	if !validInventory(pkg, inv) || inv.PolicyDigest != pd {
		return errors.New("approval inventory")
	}
	generated, err := taskscript.Generate(pkg, pd)
	if err != nil {
		return err
	}
	scripts := make([]string, len(inv.Tasks))
	for i, task := range inv.Tasks {
		for _, canonical := range generated {
			if canonical.Kind == task.Kind {
				scripts[i] = canonical.Script
			}
		}
	}
	return readBackTasks(ctx, d, inv.Tasks, scripts, true)
}

// uninstallDeletesState reads the uninstall answer the package account
// writes before its purge request.
func uninstallDeletesState(path string) bool {
	b, _, err := readLifecycleFile(path, 64)
	return err == nil && strings.TrimSuffix(string(b), "\n") == "delete-state"
}

// uninstallApproval is live while the package still ships the approved pins
// and both approval tasks of that policy stand unchanged.
func uninstallApproval(d *liveDSM, pkg, target, runtime, executorDigest, policyDigest string) func(context.Context) error {
	return func(ctx context.Context) error {
		if !samePackagePins(ctx, target, executorDigest, policyDigest) {
			return errors.New("package pins")
		}
		inv, err := readTaskInventory(runtime)
		if err != nil {
			return err
		}
		return liveTasks(ctx, d, pkg, policyDigest, inv)
	}
}
