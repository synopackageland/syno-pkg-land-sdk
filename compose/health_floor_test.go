package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartupBudgetDefaultToleratesSlowNAS(t *testing.T) {
	p, err := Build(context.Background(), upstreamProject(t, "openlist"))
	if err != nil {
		t.Fatal(err)
	}
	if p.StartupTimeoutSeconds != 900 {
		t.Fatalf("default startup budget %d is too short for a loaded HDD NAS", p.StartupTimeoutSeconds)
	}
}

// Docker health is what Container Manager displays and what depends_on
// service_healthy waits for. A busy NAS must not be reported unhealthy in seconds.
func TestHealthcheckTimingHasSlowNASFloor(t *testing.T) {
	r := upstreamProject(t, "immich")
	strict := []byte(`
services:
  redis:
    healthcheck:
      test: redis-cli ping | grep -q PONG || exit 1
      interval: 5s
      timeout: 3s
      retries: 1
      start_period: 10s
  immich-machine-learning:
    healthcheck:
      disable: true
`)
	if err := os.WriteFile(filepath.Join(r.Root, "strict.yaml"), strict, 0600); err != nil {
		t.Fatal(err)
	}
	r.Files = append(r.Files, "strict.yaml")
	seconds := 600
	r.StartupTimeoutSeconds = &seconds
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	redis := p.Effective.Services["redis"].HealthCheck
	if redis == nil || len(redis.Test) == 0 || redis.Test[0] != "CMD-SHELL" {
		t.Fatalf("App health command lost: %+v", redis)
	}
	if time.Duration(*redis.Timeout) != 30*time.Second || time.Duration(*redis.Interval) != 5*time.Second || *redis.Retries != 3 || time.Duration(*redis.StartPeriod) != 600*time.Second {
		t.Fatalf("strict timing kept: timeout=%v interval=%v retries=%v start=%v", *redis.Timeout, *redis.Interval, *redis.Retries, *redis.StartPeriod)
	}
	// An image HEALTHCHECK is inherited; only its timing receives the floor.
	server := p.Effective.Services["immich-server"].HealthCheck
	if server == nil || len(server.Test) != 0 || server.Disable || time.Duration(*server.Timeout) != 30*time.Second || time.Duration(*server.StartPeriod) != 600*time.Second {
		t.Fatalf("image health timing: %+v", server)
	}
	if ml := p.Effective.Services["immich-machine-learning"].HealthCheck; ml == nil || !ml.Disable || ml.Timeout != nil {
		t.Fatalf("explicitly disabled health changed: %+v", ml)
	}
}

func TestHealthcheckTimingKeepsMoreTolerantAppValues(t *testing.T) {
	r := upstreamProject(t, "immich")
	lenient := []byte(`
services:
  redis:
    healthcheck:
      test: redis-cli ping | grep -q PONG || exit 1
      interval: 2m
      timeout: 90s
      retries: 10
      start_period: 1h
`)
	os.WriteFile(filepath.Join(r.Root, "lenient.yaml"), lenient, 0600)
	r.Files = append(r.Files, "lenient.yaml")
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	h := p.Effective.Services["redis"].HealthCheck
	if time.Duration(*h.Timeout) != 90*time.Second || time.Duration(*h.Interval) != 2*time.Minute || *h.Retries != 10 || time.Duration(*h.StartPeriod) != time.Hour {
		t.Fatalf("tolerant App values reduced: %+v", h)
	}
}
