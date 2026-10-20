package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComposeBudgetInShippedController(t *testing.T) {
	for _, mode := range []string{"settings", "host"} {
		t.Run(mode, func(t *testing.T) {
			control, dest, data := fixture(t, "normal")
			path := filepath.Join(dest, "daemon/config.json")
			raw, _ := os.ReadFile(path)
			var cfg config
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			cfg.Network = &networkPolicy{Mode: mode, Ports: []int{65535}}
			if mode == "settings" {
				cfg.Timeouts.Start = 600
			}
			raw, _ = json.Marshal(cfg)
			if err := os.WriteFile(path, raw, 0644); err != nil {
				t.Fatal(err)
			}
			rc, out := runControl(control, "start", dest, data)
			if mode == "settings" {
				if rc != 0 {
					t.Fatalf("long budget rejected: %d %s", rc, out)
				}
			} else {
				if rc != 1 || !strings.Contains(out, "COMPOSE_STARTUP_TIMEOUT") || !strings.Contains(out, "phase=COMPOSE_NETWORK_NOT_READY") {
					t.Fatalf("%d %s", rc, out)
				}
				log, _ := os.ReadFile(filepath.Join(data, "daemon/daemon.log"))
				if strings.Contains(string(log), `"uid":`) {
					t.Fatal("child started before readiness")
				}
			}
		})
	}
}

func TestStartupWaitKeepsLastPhaseAndNeverLeaksProbeErrors(t *testing.T) {
	err := waitReadiness(time.Now().Add(30*time.Millisecond), make(chan os.Signal), func() error {
		return errors.New("COMPOSE_HEALTH_NOT_READY: http://user:secret@private/?token=secret")
	})
	if err == nil || !strings.Contains(err.Error(), "phase=COMPOSE_HEALTH_NOT_READY") || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}

func TestStartupWaitAllowsDelayedReadyAndCancellation(t *testing.T) {
	calls := 0
	err := waitReadiness(time.Now().Add(time.Second), make(chan os.Signal), func() error {
		calls++
		if calls < 2 {
			return errors.New("COMPOSE_TCP_NOT_READY")
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("%d %v", calls, err)
	}
	stop := make(chan os.Signal)
	close(stop)
	if err := waitReadiness(time.Now().Add(time.Second), stop, func() error { return errors.New("secret") }); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}

func TestStartupBudgetCapsAreValidated(t *testing.T) {
	for _, network := range []*networkPolicy{nil, {Mode: "bridge"}} {
		for _, seconds := range []int{120, 121, 900, 901} {
			c := config{Network: network, Timeouts: deadlines{Start: seconds, Stop: 30, Status: 5, Uninstall: 60}}
			want := seconds <= 3600 && (network == nil || seconds <= 900)
			if (validateDeadlines(c) == nil) != want {
				t.Fatalf("network=%v seconds=%d", network, seconds)
			}
		}
	}
}
