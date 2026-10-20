package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type lifecycleMonitor struct {
	pkg, packageDir, control string
	initialStop              os.FileInfo
	lastControl              os.FileInfo
	lastValue                string
	log                      func(string)
	now                      func() time.Time
	missingSince             time.Time
	unavailable              bool
}

const missingPackageGrace = 10 * time.Minute

func newLifecycleMonitor(pkg, packageDir, control string, log func(string)) *lifecycleMonitor {
	m := &lifecycleMonitor{pkg: pkg, packageDir: packageDir, control: control, log: log, now: time.Now}
	// An approved boot/admin start resumes a previous stop. Capture before the
	// socket is exposed; every subsequent atomic request has a different inode.
	if b, info, err := readLifecycleFile(control, 64); err == nil && (string(b) == "stop\n" || string(b) == "stop") {
		m.initialStop = info
	}
	return m
}

// A supervisor restart carries no new approval, so a stop already in the
// mailbox stays in force.
func newRevivedLifecycleMonitor(pkg, packageDir, control string, log func(string)) *lifecycleMonitor {
	m := newLifecycleMonitor(pkg, packageDir, control, log)
	m.initialStop = nil
	return m
}

// DSM's var link is expected. Resolve its parent, then reject final symlinks,
// FIFOs and oversized input. Linux openRegular pins all resolved components.
func readLifecycleFile(path string, limit int) ([]byte, os.FileInfo, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, nil, err
	}
	f, err := openRegular(filepath.Join(parent, filepath.Base(path)))
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	b, err := readLimited(f, limit)
	return b, info, err
}
func (m *lifecycleMonitor) watch(ctx context.Context, interval time.Duration) string {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastIssue := ""
	lastInvalid := ""
	report := func(issue string) {
		if issue != lastIssue {
			m.log(issue)
		}
		lastIssue = issue
	}
	for {
		now := m.now()
		available, missing := false, false
		info, err := os.Stat(m.packageDir)
		if os.IsNotExist(err) || err == nil && !info.IsDir() {
			missing = true
		} else if err != nil {
			report("cannot inspect package directory")
		} else {
			b, _, err := readLifecycleFile(filepath.Join(m.packageDir, "INFO"), 65536)
			if os.IsNotExist(err) {
				missing = true
			} else if err != nil {
				report("cannot read package INFO")
			} else {
				matches, mismatch := 0, false
				for _, line := range strings.Split(string(b), "\n") {
					if strings.HasPrefix(line, "package=") {
						if line != "package=\""+m.pkg+"\"" {
							mismatch = true
						}
						matches++
					}
				}
				available = matches == 1 && !mismatch
				missing = !available
			}
		}
		// Keep this timestamp on the monitor: watch returns for actions while
		// the root process continues polling, including when already paused.
		if missing {
			if m.missingSince.IsZero() {
				m.missingSince = now
			}
		} else {
			// An unreadable path cannot confirm continuous absence either.
			m.missingSince = time.Time{}
		}
		if available && m.unavailable {
			m.unavailable = false
			// The listener was paused independently of the mailbox. Re-read
			// even an unchanged start, and honor a stop captured before boot.
			m.lastControl = nil
			m.lastValue = ""
			m.initialStop = nil
		}
		b, controlInfo, controlErr := readLifecycleFile(m.control, 64)
		if controlErr == nil {
			action := strings.TrimSuffix(string(b), "\n")
			switch action {
			case "purge":
				m.log("lifecycle control: purge")
				return "purge"
			case "stop", "start":
				if !available {
					break
				}
				if m.lastControl != nil && os.SameFile(m.lastControl, controlInfo) && m.lastControl.ModTime().Equal(controlInfo.ModTime()) && m.lastValue == action {
					break
				}
				old := m.initialStop
				if action == "start" || old == nil || !os.SameFile(old, controlInfo) || !old.ModTime().Equal(controlInfo.ModTime()) || old.Size() != controlInfo.Size() {
					m.lastControl = controlInfo
					m.lastValue = action
					m.log("lifecycle control: " + action)
					return action
				}
			default:
				if string(b) != lastInvalid || lastInvalid == "" {
					m.log(fmt.Sprintf("ignored invalid lifecycle control (%d bytes)", len(b)))
				}
				lastInvalid = string(b)
			}
		} else if !os.IsNotExist(controlErr) {
			report("ignored unreadable lifecycle control")
		}
		if missing && now.Sub(m.missingSince) >= missingPackageGrace {
			m.log("package identity absent for 10 minutes: purge")
			return "purge"
		}
		if !available && !m.unavailable {
			m.unavailable = true
			m.log("package identity unavailable: paused")
			return "stop"
		}
		select {
		case <-ctx.Done():
			return "stop"
		case <-ticker.C:
		}
	}
}
