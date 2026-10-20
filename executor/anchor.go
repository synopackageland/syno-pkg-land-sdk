package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// Package Center writes the SPK INFO as root. The package account cannot change
// it, so its executor digest authorizes executor-only upgrades under the
// administrator-approved policy without a new PasswordConfirm.
const executorAnchorKey = "synopkgland_executor_sha256"

var anchorRE = regexp.MustCompile(`^"([a-f0-9]{64})"$`)

type pinState int

const (
	pinsDiffer pinState = iota
	pinsMatch
	pinsAnchored
)

func executorAnchor(info string, trusted func(os.FileInfo) bool) (string, error) {
	b, stat, err := readLifecycleFile(info, 65536)
	if err != nil {
		return "", err
	}
	if !trusted(stat) {
		return "", errors.New("untrusted package INFO")
	}
	anchor := ""
	for _, line := range strings.Split(string(b), "\n") {
		value, ok := strings.CutPrefix(line, executorAnchorKey+"=")
		if !ok {
			continue
		}
		m := anchorRE.FindStringSubmatch(value)
		if m == nil || anchor != "" {
			return "", errors.New("executor anchor")
		}
		anchor = m[1]
	}
	if anchor == "" {
		return "", errors.New("executor anchor")
	}
	return anchor, nil
}

func rootOwnedReadOnly(info os.FileInfo) bool {
	return ownedByRoot(info) && info.Mode().Perm()&0022 == 0
}

func packagePins(ctx context.Context, dir, info string, trusted func(os.FileInfo) bool, executorDigest, policyDigest string) pinState {
	if samePackagePins(ctx, dir, executorDigest, policyDigest) {
		return pinsMatch
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return pinsDiffer
	}
	if pd, err := digestFile(ctx, dir+"/policy.json", maxPolicyBytes); err != nil || pd != policyDigest {
		return pinsDiffer
	}
	anchor, err := executorAnchor(info, trusted)
	if err != nil {
		return pinsDiffer
	}
	if ed, err := digestFile(ctx, dir+"/executor", maxExecutorBytes); err != nil || ed != anchor {
		return pinsDiffer
	}
	return pinsAnchored
}

// The policy is unchanged, so root scripts (pinned by policy digests) stay too.
// Verify the protected copy, never the package-reachable source.
func installAnchored(ctx context.Context, source, info string, trusted func(os.FileInfo) bool, runtime string) error {
	anchor, err := executorAnchor(info, trusted)
	if err != nil {
		return err
	}
	// DSM links the package target to the volume; openRegular refuses links.
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	src, err := openRegular(filepath.Join(source, "executor"))
	if err != nil {
		return err
	}
	defer src.Close()
	stat, err := src.Stat()
	if err != nil || stat.Size() > maxExecutorBytes {
		return errors.New("executor size")
	}
	staged, err := os.CreateTemp(runtime, ".executor-")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	err = copyExact(staged, src, stat.Size())
	if err == nil {
		err = staged.Chmod(0700)
	}
	if err == nil {
		err = staged.Sync()
	}
	if closeErr := staged.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if digest, err := digestFile(ctx, staged.Name(), maxExecutorBytes); err != nil || digest != anchor {
		return errors.New("executor anchor mismatch")
	}
	return os.Rename(staged.Name(), filepath.Join(runtime, "executor"))
}

// installAnchored stages as .executor-*; control.sh's .executor is its own.
func removeStagedExecutors(runtime string) {
	staged, _ := filepath.Glob(filepath.Join(runtime, ".executor-*"))
	for _, name := range staged {
		os.Remove(name)
	}
}

// Root control start replaces this monitor with the newly installed runtime.
func startInstalled(runtime, pkg string) error {
	cmd := exec.Command(filepath.Join(runtime, "executor"), "control", "start", "--package", pkg)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// An anchored upgrade is handed to root start, which replaces this monitor:
// only differing pins are an approval request, and only they say so.
func approvedPins(state pinState, install, start func() error, log func(string)) bool {
	if state == pinsDiffer {
		log("executor paused: package pins require administrator approval")
	}
	if state != pinsAnchored {
		return state == pinsMatch
	}
	if err := install(); err != nil {
		log("executor upgrade refused: INFO-anchored executor could not be installed")
		return false
	}
	if err := start(); err != nil {
		log("executor upgrade installed; root start failed")
		return false
	}
	log("executor upgrade: INFO-anchored executor installed under the approved policy")
	return false
}
