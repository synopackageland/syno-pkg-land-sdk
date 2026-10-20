package compose

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestStartupBudgetReachesBootstrapWithoutChangingUpstream(t *testing.T) {
	for _, seconds := range []int{120, 600, 900} {
		r := upstreamProject(t, "openlist")
		if seconds != 120 {
			r.StartupTimeoutSeconds = &seconds
		}
		// A service override cannot quietly give the hook a different deadline.
		policy := r.Services["openlist"]
		policy.Environment = map[string]string{"SYNOPKGLAND_STARTUP_BUDGET_SECONDS": "9999"}
		r.Services["openlist"] = policy
		p, err := Build(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if p.StartupTimeoutSeconds != seconds {
			t.Fatal(p.StartupTimeoutSeconds)
		}
		v := p.Effective.Services["openlist"].Environment["SYNOPKGLAND_STARTUP_BUDGET_SECONDS"]
		if v == nil || *v != strconv.Itoa(seconds) {
			t.Fatalf("bootstrap budget: %v", v)
		}
	}
}

func TestStartupBudgetRejectsUnboundedValues(t *testing.T) {
	for _, seconds := range []int{-1, 0, 1, 901} {
		r := upstreamProject(t, "openlist")
		r.StartupTimeoutSeconds = &seconds
		if _, err := Build(context.Background(), r); err == nil || !strings.Contains(err.Error(), "COMPOSE_STARTUP_INVALID") {
			t.Fatalf("accepted %d: %v", seconds, err)
		}
	}
}
