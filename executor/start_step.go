package main

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"time"
)

// The start step delays only the executor's readiness, never the package
// start, which returns once it wrote its request, nor the listener.
const startStepTimeout = 60 * time.Second

const startRecordName = "start.record"

var versionRE = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+~-]{0,63}$`)

// startRecord is the package version the executor last started for, with
// the start step's result for that version.
type startRecord struct {
	Version string           `json:"version"`
	Step    *startStepResult `json:"step,omitempty"`
}

type startStepResult struct {
	Reason          string    `json:"reason"`
	PreviousVersion string    `json:"previous_version,omitempty"`
	Exit            string    `json:"exit"`
	Output          string    `json:"output"`
	FinishedAt      time.Time `json:"finished_at"`
}

func (s *Store) StartRecord() (startRecord, error) {
	var r startRecord
	err := s.record(startRecordName, &r)
	return r, err
}

func (s *Store) SetStartRecord(r startRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.atomic(startRecordName, b)
}

// rootStart is the App's own root step at the first start after an install
// or upgrade: the op the approved policy names as start, consented to by
// that approval.
type rootStart struct {
	Approved func(context.Context) error
	Runner   RootRunner
	State    string
	Store    *Store
}

// run never stops the start. The step runs once per package version: its
// argument is install, or upgrade and the version started before. A step
// skipped without approval or interrupted runs at the next start.
func (r rootStart) run(ctx context.Context, p Policy, version string, log func(string)) {
	if version == "" {
		log("start root step skipped: package version unknown: package=" + p.Package)
		return
	}
	previous, err := r.Store.StartRecord()
	if err != nil && !os.IsNotExist(err) {
		log("start record unreadable: package=" + p.Package + ": " + err.Error())
		return
	}
	if err == nil && previous.Version == version {
		return
	}
	next := startRecord{Version: version}
	if p.Start != "" {
		args := []string{"install"}
		if err == nil {
			args = []string{"upgrade", previous.Version}
		}
		exit, out := runApprovedStep(ctx, r.Approved, r.Runner, p, p.Start, "start", args, r.State, lifecycleStepBudget(p, "start"), log)
		if exit == "" || ctx.Err() != nil {
			return
		}
		next.Step = &startStepResult{Reason: args[0], Exit: exit, Output: out, FinishedAt: time.Now().UTC()}
		if len(args) > 1 {
			next.Step.PreviousVersion = args[1]
		}
	}
	if err := r.Store.SetStartRecord(next); err != nil {
		log("start record not written: package=" + p.Package + ": " + err.Error())
	}
}

// due reports whether run would start the policy's step for version.
func (r rootStart) due(p Policy, version string) bool {
	if p.Start == "" || version == "" {
		return false
	}
	previous, err := r.Store.StartRecord()
	return os.IsNotExist(err) || err == nil && previous.Version != version
}

// openStarted listens first. A start step due for this version runs behind
// the socket once the task inventory is read back; until it ends, status
// answers starting and every other request is refused, so the step still
// precedes every session request. Closing interrupts the step.
func (e *Executor) openStarted(ctx context.Context, listen func() (func(), error), inventory <-chan struct{}, step rootStart, log func(string)) (func(), error) {
	e.starting.Store(step.due(e.Policy, e.PackageVersion))
	closeService, err := listen()
	if err != nil {
		e.starting.Store(false)
		return nil, err
	}
	stepCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-inventory:
		case <-stepCtx.Done():
			return
		}
		step.run(stepCtx, e.Policy, e.PackageVersion, log)
		e.starting.Store(false)
	}()
	return func() { cancel(); <-done; closeService() }, nil
}

// startStepStatus is the start step of version as status reports it:
// pending until the step ran for that version, then ok or failed.
func startStepStatus(s *Store, version string) map[string]any {
	r, err := s.StartRecord()
	if err != nil || r.Version != version || r.Step == nil {
		return map[string]any{"state": "pending"}
	}
	state := "failed"
	if r.Step.Exit == "0" {
		state = "ok"
	}
	out := map[string]any{"state": state, "reason": r.Step.Reason, "exit": r.Step.Exit, "output": r.Step.Output, "finished_at": r.Step.FinishedAt.Format(time.RFC3339)}
	if r.Step.PreviousVersion != "" {
		out["previous_version"] = r.Step.PreviousVersion
	}
	return out
}

// packageVersion reads the one version line of the package INFO; it is
// empty when INFO has none, several, or an unusual one.
func packageVersion(info string) string {
	b, _, err := readLifecycleFile(info, 65536)
	if err != nil {
		return ""
	}
	version := ""
	for _, line := range strings.Split(string(b), "\n") {
		value, ok := strings.CutPrefix(line, "version=")
		if !ok {
			continue
		}
		if version != "" {
			return ""
		}
		version = strings.Trim(value, `"`)
		if !versionRE.MatchString(version) {
			return ""
		}
	}
	return version
}
