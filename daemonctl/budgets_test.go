package main

import (
	"fmt"
	"net/http"
	"testing"
	"time"
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
