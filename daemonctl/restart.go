package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Only the directly owned foreground child is restarted. This is not a second
// package/service manager and never recovers a lost supervisor from inventory.
type restartPolicy struct {
	Mode           string `json:"mode"`
	FatalExitCodes []int  `json:"fatal_exit_codes,omitempty"`
	InitialSeconds int    `json:"initial_seconds,omitempty"`
	MaxSeconds     int    `json:"max_seconds,omitempty"`
	MaxAttempts    int    `json:"max_attempts,omitempty"`
	WindowSeconds  int    `json:"window_seconds,omitempty"`
}

func normalizeRestart(p *restartPolicy) (*restartPolicy, error) {
	if p == nil {
		p = &restartPolicy{Mode: "on-failure"}
	}
	v := *p
	if v.Mode != "off" && v.Mode != "on-failure" {
		return nil, errors.New("invalid daemon restart mode: use off or on-failure")
	}
	if v.InitialSeconds == 0 {
		v.InitialSeconds = 2
	}
	if v.MaxSeconds == 0 {
		v.MaxSeconds = 300
	}
	if v.MaxAttempts == 0 {
		v.MaxAttempts = 8
	}
	if v.WindowSeconds == 0 {
		v.WindowSeconds = 600
	}
	if v.InitialSeconds < 1 || v.InitialSeconds > 3600 || v.MaxSeconds < v.InitialSeconds || v.MaxSeconds > 3600 || v.MaxAttempts < 1 || v.MaxAttempts > 100 || v.WindowSeconds < 1 || v.WindowSeconds > 86400 {
		return nil, errors.New("invalid daemon restart limits: delays 1-3600s, max >= initial, attempts 1-100, window 1-86400s")
	}
	for _, code := range v.FatalExitCodes {
		if code < 1 || code > 255 {
			return nil, errors.New("fatal exit codes must be 1-255")
		}
	}
	return &v, nil
}

// supervisorLog writes one supervisor line to w, after the UTC time: the line
// lands in daemon.log beside the child's output, which has times of its own.
func supervisorLog(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "%s "+format+"\n", append([]any{time.Now().UTC().Format(time.RFC3339)}, args...)...)
}

func (c *controller) writeRecord(r record) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	path := filepath.Join(c.dir, "daemon.pid")
	// Never replace foreign inventory. Stage and sync the next generation before
	// rename so a crash or a simultaneous reader cannot observe a truncated PID.
	if _, err := os.Lstat(path); err == nil {
		old, err := safeFileExisting(path)
		if err != nil {
			return err
		}
		old.Close()
	} else if !os.IsNotExist(err) {
		return err
	}
	next := filepath.Join(c.dir, "daemon.next")
	f, err := safeFile(next)
	if err != nil {
		return err
	}
	if err = f.Truncate(0); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(next, path); err != nil {
		return err
	}
	dir, err := os.Open(c.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// A saved reason is diagnostic inventory, not proof of live readiness.
func (c *controller) savedReason() string {
	r, err := c.readRecord()
	if err == nil {
		switch r.Reason {
		case "DAEMON_RESTART_LIMIT", "DAEMON_CHILD_EXIT", "DAEMON_CHILD_FAILED", "DAEMON_PERMANENT_ERROR", "DAEMON_CLEANUP_REQUIRED":
			return r.Reason
		}
	}
	return "daemon failed or supervisor unavailable"
}

func (c *controller) statusJSON(until time.Time) (int, error) {
	if err := c.request("snapshot", until); err == nil {
		// request prints the authenticated snapshot and returns its state below.
		return 0, nil
	} else {
		var state *snapshotError
		if errors.As(err, &state) {
			return 1, nil
		}
	}
	r, err := c.readRecord()
	if os.IsNotExist(err) {
		r = record{State: "stopped", Digest: c.digest}
		owner, lockErr := c.lock("owner.lock", time.Now())
		if lockErr != nil {
			r.State, r.Reason = "unavailable", "DAEMON_SUPERVISOR_UNAVAILABLE"
		} else {
			owner.Close()
		}
	} else if err != nil {
		r = record{State: "unavailable", Reason: "DAEMON_SUPERVISOR_UNAVAILABLE", Digest: c.digest}
	} else if r.State != "failed" && r.State != "exited" {
		r.State, r.Reason = "unavailable", "DAEMON_SUPERVISOR_UNAVAILABLE"
	}
	json.NewEncoder(os.Stdout).Encode(r)
	if r.State == "stopped" {
		return 3, nil
	}
	return 1, nil
}

type snapshotError struct{}

func (*snapshotError) Error() string { return "daemon is not ready" }
