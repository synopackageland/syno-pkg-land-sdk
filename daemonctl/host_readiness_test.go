//go:build linux

package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestCompiledHostSupervisorWaitsForAppHealth(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("run as the package UID in a disposable container")
	}
	control, dest, data := fixture(t, "normal")
	probed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case probed <- struct{}{}:
		default:
		}
		w.WriteHeader(503)
	}))
	defer server.Close()
	address := server.Listener.Addr().String()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	path := filepath.Join(dest, "daemon/config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Timeouts.Start = 7
	cfg.Network = &networkPolicy{Mode: "host", Ports: []int{port}, Readiness: &bridgeReadiness{Health: healthProbe{Path: "/health", Status: 204}}}
	raw, _ = json.Marshal(cfg)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	type result struct {
		rc  int
		out string
	}
	started := make(chan result, 1)
	go func() { rc, out := runControl(control, "start", dest, data); started <- result{rc, out} }()
	select {
	case <-probed:
	case got := <-started:
		t.Fatalf("start returned before App health: %+v", got)
	case <-time.After(4 * time.Second):
		t.Fatal("App health was not probed")
	}
	server.Close()
	select {
	case got := <-started:
		t.Fatalf("start returned during listener gap: %+v", got)
	case <-time.After(200 * time.Millisecond):
	}
	ln, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	final := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	final.Listener.Close()
	final.Listener = ln
	final.Start()
	defer final.Close()
	select {
	case got := <-started:
		if got.rc != 0 {
			t.Fatalf("start: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start did not finish after health")
	}
	if rc, out := runControl(control, "status", dest, data); rc != 0 {
		t.Fatalf("status: %d %s", rc, out)
	}
	final.Close()
	until := time.Now().Add(4 * time.Second)
	for {
		if rc, _ := runControl(control, "status", dest, data); rc == 3 {
			break
		}
		if !time.Now().Before(until) {
			t.Fatal("missing ready listener did not stop the gateway")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestHostReadinessWaitsForHealthAcrossListenerHandoff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	address := server.Listener.Addr().String()
	_, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	c := &controller{config: config{Network: &networkPolicy{Mode: "host", Ports: []int{port}, Readiness: &bridgeReadiness{Health: healthProbe{Path: "/health", Status: 204}}}}}
	var final *httptest.Server
	defer func() {
		if final != nil {
			final.Close()
		}
	}()
	calls := 0
	err := waitReadiness(time.Now().Add(3*time.Second), make(chan os.Signal), func() error {
		calls++
		err := c.checkReadiness()
		switch calls {
		case 1:
			if err == nil {
				t.Fatal("temporary startup listener admitted before App health")
			}
			server.Close()
		case 2:
			if err == nil {
				t.Fatal("listener gap admitted")
			}
			ln, listenErr := net.Listen("tcp4", address)
			if listenErr != nil {
				t.Fatal(listenErr)
			}
			final = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health" {
					t.Error(r.URL.Path)
				}
				w.WriteHeader(204)
			}))
			final.Listener.Close()
			final.Listener = ln
			final.Start()
		}
		return err
	})
	if err != nil || calls != 3 {
		t.Fatalf("probes=%d: %v", calls, err)
	}
	final.Close()
	err = c.checkReadiness()
	var window healthWindow
	if err == nil || !window.observe(time.Now(), err, time.Minute) {
		t.Fatalf("lost ready listener was not fatal: %v", err)
	}
}

func TestHostHealthValidationAndRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/healthy")
		w.WriteHeader(302)
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	c := &controller{config: config{Network: &networkPolicy{Mode: "host", Ports: []int{port}, Readiness: &bridgeReadiness{Health: healthProbe{Path: "/health", Status: 200}}}}}
	if err := c.checkReadiness(); err == nil || readinessPhase(err) != "COMPOSE_HEALTH_NOT_READY" {
		t.Fatalf("redirect: %v", err)
	}
	for _, path := range []string{"", "//other.invalid/", "http://other.invalid/", "/health#fragment", "/\r\nX: x"} {
		c.Network.Readiness.Health.Path = path
		if err := c.checkReadiness(); err == nil || readinessPhase(err) != "COMPOSE_READINESS_INVALID" {
			t.Fatalf("path %q: %v", path, err)
		}
	}
	c.Network.Readiness.Health = healthProbe{Path: "/health", Status: 503}
	if err := c.checkReadiness(); err == nil || readinessPhase(err) != "COMPOSE_READINESS_INVALID" {
		t.Fatalf("status: %v", err)
	}
}
