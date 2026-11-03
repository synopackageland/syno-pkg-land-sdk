//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestCompiledHostSupervisorKeepsStartingAndRecovers(t *testing.T) {
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
	// Startup acknowledges ownership while the gateway remains closed to ingress.
	if rc, out := runControl(control, "start", dest, data); rc != 0 {
		t.Fatalf("start: %d %s", rc, out)
	}
	defer runControl(control, "stop", dest, data)
	select {
	case <-probed:
	case <-time.After(4 * time.Second):
		t.Fatal("health was not probed")
	}
	if rc, out := runControl(control, "status", dest, data); rc != 0 {
		t.Fatalf("starting package is not running: %d %s", rc, out)
	}
	server.Close()
	// A missing listener is availability, not a policy or ownership violation.
	time.Sleep(1500 * time.Millisecond)
	if rc, out := runControl(control, "status", dest, data); rc != 0 {
		t.Fatalf("listener gap stopped gateway: %d %s", rc, out)
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
	until := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(filepath.Join(data, "daemon/daemon.pid"))
		var record record
		json.Unmarshal(raw, &record)
		conn, err := net.Dial("unix", filepath.Join(data, "daemon/ctl.sock"))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(conn, "status %s\n", record.Digest)
		conn.SetReadDeadline(until)
		line, _ := bufio.NewReader(conn).ReadString('\n')
		conn.Close()
		if line == "ok\n" {
			break
		}
		if !time.Now().Before(until) {
			t.Fatalf("recovery never opened ingress: %q", line)
		}
		time.Sleep(100 * time.Millisecond)
	}
	final.Close()
	time.Sleep(1500 * time.Millisecond)
	if rc, out := runControl(control, "status", dest, data); rc != 0 {
		t.Fatalf("slow/missing App stopped gateway: %d %s", rc, out)
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
	if err == nil || window.observe(time.Now(), err, time.Minute) {
		t.Fatalf("lost ready listener must stay starting: %v", err)
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
