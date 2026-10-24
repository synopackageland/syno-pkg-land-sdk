package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeimages"
)

func TestReadinessAllowsSlowHealthWithinOperationBudget(t *testing.T) {
	c, close := readinessController(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1100 * time.Millisecond)
		w.WriteHeader(204)
	}))
	defer close()
	c.Timeouts.Status = 3
	if err := c.recordAcquire(true); err != nil {
		t.Fatal(err)
	}
	if err := c.checkReadiness(); err != nil {
		t.Fatalf("single slow health response killed availability: %v", err)
	}
}

func TestLifecycleBudgetsAcceptLongNativeDeadlines(t *testing.T) {
	if err := validateDeadlines(config{Timeouts: deadlines{Start: 600, Stop: 600, Status: 60, Uninstall: 600}}); err != nil {
		t.Fatal(err)
	}
}

func TestHealthWindowNeverToleratesAuthorityFailure(t *testing.T) {
	now := time.Now()
	var window healthWindow
	if window.observe(now, fmt.Errorf("COMPOSE_HEALTH_NOT_READY"), time.Second) {
		t.Fatal("single failure stopped App")
	}
	if window.observe(now.Add(time.Second/2), nil, time.Second) {
		t.Fatal("recovery refused")
	}
	if !window.observe(now, fmt.Errorf("COMPOSE_ACQUIRE_GENERATION_INVALID"), time.Second) {
		t.Fatal("authority failure tolerated")
	}
}

// One health request on a loaded NAS can take tens of seconds. The DSM status
// command's own short budget must not become the App probe timeout.
func TestHealthProbeIsNotSecondsLevel(t *testing.T) {
	c := &controller{config: config{Timeouts: deadlines{Start: 900, Stop: 30, Status: 5, Uninstall: 60}}}
	if got := c.probeBudget(); got < 30*time.Second {
		t.Fatalf("probe budget %v", got)
	}
}

// An admitted App that stalls gets the same time to recover as it had to start;
// only then does the supervisor stop its gateway.
func TestAdmittedAppGetsStartupBudgetToRecover(t *testing.T) {
	c := &controller{config: config{Timeouts: deadlines{Start: 900, Stop: 30, Status: 5, Uninstall: 60}}}
	if got := c.recoveryBudget(); got != 900*time.Second {
		t.Fatalf("recovery budget %v", got)
	}
	var window healthWindow
	now := time.Now()
	slow := fmt.Errorf("COMPOSE_HEALTH_NOT_READY")
	if window.observe(now, slow, c.recoveryBudget()) || window.observe(now.Add(10*time.Minute), slow, c.recoveryBudget()) {
		t.Fatal("stalled App stopped inside its startup budget")
	}
	if !window.observe(now.Add(901*time.Second), slow, c.recoveryBudget()) {
		t.Fatal("App unavailable beyond its startup budget kept running")
	}
	short := &controller{config: config{Timeouts: deadlines{Start: 10, Stop: 30, Status: 5, Uninstall: 60}}}
	if short.recoveryBudget() != short.operationBudget() {
		t.Fatal("recovery budget below the operation budget")
	}
}

func TestStatusReportsStartingWhileAppRecovers(t *testing.T) {
	if statusLine(true) != "ok\n" || statusLine(false) != "starting\n" {
		t.Fatal(statusLine(true), statusLine(false))
	}
	for line, running := range map[string]bool{"ok\n": true, "starting\n": true, "unknown\n": false, "failed\n": false} {
		if statusRunning(line) != running {
			t.Fatalf("%q", line)
		}
	}
}

func TestStalledAppFailureMessageIsClear(t *testing.T) {
	err := recoveryFailure(fmt.Errorf("COMPOSE_HEALTH_NOT_READY: http://secret@private/"), 900*time.Second)
	if !strings.Contains(err.Error(), "COMPOSE_HEALTH_NOT_READY") || !strings.Contains(err.Error(), "900s") || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}

func TestReadyComposeAppRemembersItsLockedImages(t *testing.T) {
	dest, data := t.TempDir(), t.TempDir()
	ref := "example/app@sha256:" + strings.Repeat("a", 64)
	os.MkdirAll(filepath.Join(dest, "compose"), 0700)
	os.WriteFile(filepath.Join(dest, "compose", "compose.lock.json"), []byte(`{"schema":1,"package":"Fixture","images":[{"service":"app","reference":"`+ref+`","pinned":true}]}`), 0600)
	c := &controller{config: config{Package: "Fixture"}, dest: dest, data: data, dir: data}
	c.rememberImages()
	if !composeimages.Remembered(data, "Fixture")[ref] {
		t.Fatal("ready App images not remembered")
	}
	// Non-Compose daemons have no lock; nothing is written and nothing fails.
	other := &controller{config: config{Package: "Fixture"}, dest: t.TempDir(), dir: t.TempDir()}
	other.rememberImages()
}
