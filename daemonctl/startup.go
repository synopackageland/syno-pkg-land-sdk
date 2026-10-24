package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeimages"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/startupbudget"
)

func validateDeadlines(c config) error {
	t := c.Timeouts
	if t.Start < 2 || t.Start > 3600 || t.Stop < 2 || t.Stop > 3600 || t.Status < 1 || t.Status > 3600 || t.Uninstall < 2 || t.Uninstall > 3600 {
		return errors.New("invalid daemon deadlines: start/stop/uninstall 2-3600s, status 1-3600s")
	}
	if c.Network != nil && t.Start > startupbudget.MaxSeconds {
		return errors.New("container startup budget exceeds 900s")
	}
	return nil
}

// Probe errors can contain URLs, filesystem paths and HTTP client text. Only
// fixed phase codes cross the diagnostic boundary, never the original error.
func readinessPhase(err error) string {
	for _, phase := range []string{"COMPOSE_READINESS_INVALID", "COMPOSE_ACQUIRE_GENERATION_INVALID", "COMPOSE_TCP_NOT_READY", "COMPOSE_HEALTH_NOT_READY"} {
		if strings.HasPrefix(err.Error(), phase+":") || err.Error() == phase {
			return phase
		}
	}
	return "COMPOSE_NETWORK_NOT_READY"
}

func waitReadiness(until time.Time, stop <-chan os.Signal, probe func() error) error {
	phase := "COMPOSE_NETWORK_NOT_READY"
	for {
		select {
		case <-stop:
			return errors.New("COMPOSE_STARTUP_INTERRUPTED")
		default:
		}
		if !time.Now().Before(until) {
			return fmt.Errorf("COMPOSE_STARTUP_TIMEOUT: readiness budget exhausted; phase=%s", phase)
		}
		err := probe()
		if err == nil && time.Now().Before(until) {
			return nil
		}
		if err != nil {
			phase = readinessPhase(err)
		}
		delay := min(100*time.Millisecond, max(0, time.Until(until)))
		timer := time.NewTimer(delay)
		select {
		case <-stop:
			timer.Stop()
			return errors.New("COMPOSE_STARTUP_INTERRUPTED")
		case <-timer.C:
		}
	}
}

// Only availability failures receive a window. Policy, generation and network
// ownership failures remain fatal immediately; an unknown result never admits ingress.
func transientReadiness(err error) bool {
	return err != nil && (readinessPhase(err) == "COMPOSE_TCP_NOT_READY" || readinessPhase(err) == "COMPOSE_HEALTH_NOT_READY")
}

type healthWindow struct{ failedAt time.Time }

func (w *healthWindow) observe(now time.Time, err error, budget time.Duration) bool {
	if err == nil {
		w.failedAt = time.Time{}
		return false
	}
	if !transientReadiness(err) {
		return true
	}
	if w.failedAt.IsZero() {
		w.failedAt = now
	}
	return !now.Before(w.failedAt.Add(budget))
}

// recoveryBudget is how long an admitted App may stay unavailable before the
// supervisor stops its gateway: as long as it was allowed to start.
func (c *controller) recoveryBudget() time.Duration {
	return max(c.operationBudget(), time.Duration(c.Timeouts.Start)*time.Second)
}

func recoveryFailure(err error, budget time.Duration) error {
	return fmt.Errorf("%s: App stayed unavailable for %ds after startup; its gateway was stopped", readinessPhase(err), int(budget.Seconds()))
}

// While an admitted App recovers, the package is running and its gateway shows
// a starting page; only "ok" admits traffic.
func statusLine(healthy bool) string {
	if healthy {
		return "ok\n"
	}
	return "starting\n"
}

func statusRunning(line string) bool { return line == "ok\n" || line == "starting\n" }

// rememberImages records, once readiness passed, that Container Manager runs
// every locked image. Registry checks use it to stay quiet on an offline NAS.
// It is informational: a failed write never affects startup.
func (c *controller) rememberImages() {
	lock := filepath.Join(c.dest, "compose", "compose.lock.json")
	if _, err := os.Lstat(lock); err == nil {
		_ = composeimages.Remember(lock, c.dir, c.Package)
	}
}
