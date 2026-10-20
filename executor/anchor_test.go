package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func trustAll(os.FileInfo) bool { return true }

// anchoredPackage writes a target executor/policy pair and the package INFO.
func anchoredPackage(t *testing.T, executor, policy string, anchors ...string) (target, info string) {
	t.Helper()
	pkg := realTestPath(t, t.TempDir())
	target = filepath.Join(pkg, "target", "executor")
	os.MkdirAll(target, 0755)
	os.WriteFile(filepath.Join(target, "executor"), []byte(executor), 0755)
	os.WriteFile(filepath.Join(target, "policy.json"), []byte(policy), 0644)
	text := "package=\"TestApp\"\n"
	for _, a := range anchors {
		text += "synopkgland_executor_sha256=\"" + a + "\"\n"
	}
	info = filepath.Join(pkg, "INFO")
	os.WriteFile(info, []byte(text), 0644)
	return target, info
}

func TestExecutorAnchorRequiresOneLowercaseDigestInTrustedINFO(t *testing.T) {
	digest := hashBytes([]byte("new executor"))
	_, info := anchoredPackage(t, "new executor", "policy", digest)
	if got, err := executorAnchor(info, trustAll); err != nil || got != digest {
		t.Fatal("anchor", got, err)
	}
	if _, err := executorAnchor(info, func(os.FileInfo) bool { return false }); err == nil {
		t.Fatal("package-writable INFO accepted")
	}
	for name, anchors := range map[string][]string{"missing": nil, "duplicate": {digest, digest}, "uppercase": {strings.ToUpper(digest)}, "short": {digest[:63]}} {
		_, info := anchoredPackage(t, "new executor", "policy", anchors...)
		if _, err := executorAnchor(info, trustAll); err == nil {
			t.Fatal(name, "anchor accepted")
		}
	}
}

func TestPackagePinsAcceptAnExecutorUpgradeOnlyUnderTheApprovedPolicyAndINFOAnchor(t *testing.T) {
	ctx := context.Background()
	ed, pd := hashBytes([]byte("approved executor")), hashBytes([]byte("approved policy"))
	upgraded := hashBytes([]byte("new executor"))
	for _, tc := range []struct {
		name, executor, policy string
		anchors                []string
		want                   pinState
	}{
		{"same bytes", "approved executor", "approved policy", nil, pinsMatch},
		{"anchored executor", "new executor", "approved policy", []string{upgraded}, pinsAnchored},
		{"unanchored executor", "new executor", "approved policy", nil, pinsDiffer},
		{"different anchor", "new executor", "approved policy", []string{ed}, pinsDiffer},
		{"changed policy", "new executor", "wider policy", []string{upgraded}, pinsDiffer},
		{"changed policy only", "approved executor", "wider policy", []string{ed}, pinsDiffer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, info := anchoredPackage(t, tc.executor, tc.policy, tc.anchors...)
			if got := packagePins(ctx, target, info, trustAll, ed, pd); got != tc.want {
				t.Fatal("pin state", got)
			}
		})
	}
}

func TestInstallAnchoredReplacesOnlyTheProtectedExecutorWithVerifiedBytes(t *testing.T) {
	ctx := context.Background()
	for _, anchored := range []bool{true, false} {
		t.Run(map[bool]string{true: "anchored", false: "swapped after check"}[anchored], func(t *testing.T) {
			anchor := hashBytes([]byte("new executor"))
			target, info := anchoredPackage(t, "new executor", "approved policy", anchor)
			if !anchored {
				os.WriteFile(filepath.Join(target, "executor"), []byte("package swapped"), 0755)
			}
			runtime := realTestPath(t, t.TempDir())
			os.WriteFile(filepath.Join(runtime, "executor"), []byte("approved executor"), 0700)
			os.WriteFile(filepath.Join(runtime, "policy.json"), []byte("approved policy"), 0600)
			err := installAnchored(ctx, target, info, trustAll, runtime)
			b, _ := os.ReadFile(filepath.Join(runtime, "executor"))
			if anchored && (err != nil || string(b) != "new executor") {
				t.Fatal("anchored executor not installed", err, string(b))
			}
			if !anchored && (err == nil || string(b) != "approved executor") {
				t.Fatal("unanchored executor installed", err, string(b))
			}
			if info, err := os.Stat(filepath.Join(runtime, "executor")); err != nil || info.Mode().Perm() != 0700 {
				t.Fatal("protected executor mode", err)
			}
			entries, _ := os.ReadDir(runtime)
			if len(entries) != 2 {
				t.Fatal("staging copy left in runtime", entries)
			}
		})
	}
}

// The pause message names administrator approval only when the pins differ:
// an anchored handover is a finished upgrade, not a request for approval.
func TestApprovedPinsHandOverAnAnchoredUpgradeToRootStart(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      pinState
		installErr error
		open       bool
		installs   int
		starts     int
		logged     string
	}{
		{"match", pinsMatch, nil, true, 0, 0, ""},
		{"differ", pinsDiffer, nil, false, 0, 0, "executor paused: package pins require administrator approval"},
		{"anchored", pinsAnchored, nil, false, 1, 1, "executor upgrade: INFO-anchored executor installed under the approved policy"},
		{"install failed", pinsAnchored, errors.New("digest"), false, 1, 0, "executor upgrade refused: INFO-anchored executor could not be installed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installs, starts := 0, 0
			var logged []string
			open := approvedPins(tc.state, func() error { installs++; return tc.installErr }, func() error { starts++; return nil }, func(s string) { logged = append(logged, s) })
			if open != tc.open || installs != tc.installs || starts != tc.starts {
				t.Fatal("handover", open, installs, starts)
			}
			if want := []string{tc.logged}; tc.logged == "" {
				if len(logged) != 0 {
					t.Fatal("matching pins logged", logged)
				}
			} else if !reflect.DeepEqual(logged, want) {
				t.Fatal("log", logged, want)
			}
		})
	}
}

// DSM: /var/packages/<pkg>/target links to /volume1/@appstore/<pkg>.
func TestInstallAnchoredFollowsTheDSMTargetLink(t *testing.T) {
	ctx := context.Background()
	anchor := hashBytes([]byte("new executor"))
	appstore, info := anchoredPackage(t, "new executor", "approved policy", anchor)
	link := filepath.Join(filepath.Dir(info), "linked-target")
	if err := os.Symlink(filepath.Dir(appstore), link); err != nil {
		t.Fatal(err)
	}
	runtime := realTestPath(t, t.TempDir())
	os.WriteFile(filepath.Join(runtime, "executor"), []byte("approved executor"), 0700)
	target := filepath.Join(link, "executor")
	if state := packagePins(ctx, target, info, trustAll, hashBytes([]byte("approved executor")), hashBytes([]byte("approved policy"))); state != pinsAnchored {
		t.Fatal("pin state", state)
	}
	if err := installAnchored(ctx, target, info, trustAll, runtime); err != nil {
		t.Fatal("anchored executor behind the target link refused", err)
	}
	if b, _ := os.ReadFile(filepath.Join(runtime, "executor")); string(b) != "new executor" {
		t.Fatal("installed", string(b))
	}
}

// A monitor killed mid-install (or one from 0.10.0) leaves runtime/.executor-*;
// the next monitor, holding the pid lock, is the only writer and removes it.
func TestRemoveStagedExecutorsKeepsTheRuntime(t *testing.T) {
	runtime := t.TempDir()
	for _, name := range []string{"executor", "policy.json", ".executor", ".executor-1483480695", ".executor-2"} {
		os.WriteFile(filepath.Join(runtime, name), []byte(name), 0600)
	}
	removeStagedExecutors(runtime)
	entries, _ := os.ReadDir(runtime)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if strings.Join(left, " ") != ".executor executor policy.json" {
		t.Fatal(left)
	}
}
