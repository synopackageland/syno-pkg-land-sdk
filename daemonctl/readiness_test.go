package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestReleaseAfterUpgradeRejectsMissingProfilesWithoutUsingStaleGeneration(t *testing.T) {
	t.Setenv("SYNOPKG_PKG_STATUS", "STOP")
	c, close := readinessController(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer close()
	c.dest = t.TempDir()
	c.Timeouts = deadlines{Stop: 5, Status: 1}
	if err := os.Mkdir(filepath.Join(c.dest, "compose"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.dest, "compose/compose.lock.json"), []byte(`{"schema":1,"package":"Fixture","project_name":"fixture","services":[{"service":"web"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.recordAcquire(true); err != nil {
		t.Fatal(err)
	}
	// The outgoing generation no longer matches either the installed version
	// or the new lock. Teardown must still fence the gateway before diagnosis.
	c.Network.Readiness.Version = "2.0.0-0002"
	c.Network.Readiness.PlanSHA256 = strings.Repeat("b", 64)
	if err := c.checkReadiness(); err == nil || !strings.Contains(err.Error(), "COMPOSE_ACQUIRE_GENERATION_INVALID") {
		t.Fatalf("fixture did not reproduce generation mismatch: %v", err)
	}
	rc, err := c.run("release")
	if rc == 0 || err == nil || !strings.Contains(err.Error(), "COMPOSE_STOP_UNCONFIRMED") || !strings.Contains(err.Error(), "COMPOSE_WORKER_PROFILE_MISSING") || !strings.Contains(err.Error(), "Manual Install") {
		t.Fatalf("stop falsely succeeded or lost recovery: %d %v", rc, err)
	}
	if _, err := os.Stat(filepath.Join(c.dir, "stop.request")); err != nil {
		t.Fatalf("stale generation bypassed gateway teardown: %v", err)
	}
	// Manual reinstall must remain available even though ordinary STOP fails.
	for _, status := range []string{"UPGRADE", "INSTALL", "UNINSTALL"} {
		t.Setenv("SYNOPKG_PKG_STATUS", status)
		if rc, err := c.run("release"); rc != 0 || err != nil {
			t.Fatalf("blocked %s recovery on the stale generation: %d %v", status, rc, err)
		}
	}
}

func readinessController(t *testing.T, handler http.Handler) (*controller, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	dir := t.TempDir()
	info := filepath.Join(dir, "INFO")
	os.WriteFile(info, []byte("package=\"Fixture\"\nversion=\"1.0.0-0001\"\n"), 0600)
	c := &controller{config: config{Package: "Fixture", Network: &networkPolicy{Mode: "bridge", Ports: []int{port}, Readiness: &bridgeReadiness{Version: "1.0.0-0001", PlanSHA256: strings.Repeat("a", 64), Health: healthProbe{Path: "/health", Status: 204}}}}, dir: dir, installedInfo: info}
	return c, server.Close
}

func TestBridgeRequiresHealthListenerAndCurrentLifecycleGeneration(t *testing.T) {
	c, close := readinessController(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Error(r.URL.Path)
		}
		w.WriteHeader(204)
	}))
	defer close()
	if err := c.checkReadiness(); err == nil || !strings.Contains(err.Error(), "COMPOSE_ACQUIRE_GENERATION_INVALID") {
		t.Fatalf("missing generation: %v", err)
	}
	if err := c.recordAcquire(true); err != nil {
		t.Fatal(err)
	}
	if err := c.checkReadiness(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(c.dir, "compose-acquire.json"))
	var first acquireRecord
	json.Unmarshal(raw, &first)
	if len(first.Generation) != 32 || !first.Active {
		t.Fatalf("%s", raw)
	}
	if err := c.recordAcquire(true); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(c.dir, "compose-acquire.json"))
	var next acquireRecord
	json.Unmarshal(raw, &next)
	if first.Generation == next.Generation {
		t.Fatal("lifecycle start reused generation")
	}
	if err := c.recordAcquire(false); err != nil {
		t.Fatal(err)
	}
	if err := c.checkReadiness(); err == nil {
		t.Fatal("released generation accepted")
	}
	c.recordAcquire(true)
	os.WriteFile(c.installedInfo, []byte("version=\"2.0.0-0001\"\n"), 0600)
	if err := c.checkReadiness(); err == nil {
		t.Fatal("stale installed version accepted")
	}
}

func TestBridgeHealthIsBoundedAndCannotFollowRedirects(t *testing.T) {
	c, close := readinessController(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://example.invalid/")
		w.WriteHeader(302)
	}))
	defer close()
	c.recordAcquire(true)
	if err := c.checkReadiness(); err == nil || !strings.Contains(err.Error(), "COMPOSE_HEALTH_NOT_READY") {
		t.Fatalf("%v", err)
	}
	for _, path := range []string{"//evil.invalid/", "http://evil.invalid/", "/\r\nX: x", "/health#fragment"} {
		c.Network.Readiness.Health.Path = path
		if err := c.checkReadiness(); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	c.Network.Readiness.Health.Path = "/health"
	close()
	if err := c.checkReadiness(); err == nil || !strings.Contains(err.Error(), "COMPOSE_TCP_NOT_READY") {
		t.Fatalf("%v", err)
	}
}

func TestBridgeRejectsForeignOrReplacedGenerationFiles(t *testing.T) {
	c, close := readinessController(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer close()
	c.recordAcquire(true)
	p := filepath.Join(c.dir, "compose-acquire.json")
	raw, _ := os.ReadFile(p)
	var r acquireRecord
	json.Unmarshal(raw, &r)
	r.PlanSHA256 = strings.Repeat("b", 64)
	raw, _ = json.Marshal(r)
	os.WriteFile(p, raw, 0600)
	if err := c.checkReadiness(); err == nil {
		t.Fatal("foreign plan accepted")
	}
	os.Remove(p)
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, raw, 0600)
	os.Symlink(outside, p)
	if err := c.checkReadiness(); err == nil {
		t.Fatal("symlink generation accepted")
	}
}
