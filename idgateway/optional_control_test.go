//go:build linux

package idgateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type optionalAuth struct{ admin bool }

func (a optionalAuth) Authenticate(*http.Request) (api.Identity, error) {
	return api.Identity{User: "fixture", Admin: a.admin}, nil
}

func optionalAPI(t *testing.T, cfg Config, admin bool) http.Handler {
	t.Helper()
	// The SDK reserves these controls for administrators.
	server, err := api.NewServerWithPermissions(optionalAuth{admin}, []byte(`{"schema":1,"default":"administrators","methods":{}}`), cfg.OptionalPassthroughRoutes()...)
	if err != nil {
		t.Fatal(err)
	}
	return server
}
func optionalCall(t *testing.T, server http.Handler, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest("POST", "https://nas.example:5001/api", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://nas.example:5001")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestOptionalControlsRemainAdminOnlyAndFailClosed(t *testing.T) {
	up := newUpstream(t, nil)
	_, cfg := optionalGateway(t, up.URL)
	for _, method := range []string{"gateway.passthrough.status", "gateway.passthrough.set"} {
		result := optionalCall(t, optionalAPI(t, cfg, false), `{"method":"`+method+`","params":{"id":"client","enabled":true}}`)
		if result["error"].(map[string]any)["code"] != "admin_required" {
			t.Fatalf("nonadmin: %v", result)
		}
	}
	admin := optionalAPI(t, cfg, true)
	for _, params := range []string{`{"id":"unknown","enabled":true}`, `{"id":"../escape","enabled":true}`, `{"id":"client"}`, `{"id":"client","enabled":true,"prefix":"/"}`} {
		if result := optionalCall(t, admin, `{"method":"gateway.passthrough.set","params":`+params+`}`); result["error"] == nil {
			t.Fatalf("accepted %s", params)
		}
	}
	// No gateway exists: notification fails and the on-disk state must roll back
	// to disabled, never advertise that enabled was applied.
	result := optionalCall(t, admin, `{"method":"gateway.passthrough.set","params":{"id":"client","enabled":true}}`)
	if result["error"] == nil || readOptionalState(cfg.StateDir, "client") {
		t.Fatalf("failed signal was not fail-closed: %v", result)
	}
	info, err := os.Stat(optionalStatePath(cfg.StateDir, "client"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private state: %v", err)
	}
}

func TestOptionalControlsSignalLiveGatewayAndCancelStreams(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic dXNlcjpwYXNz" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/dav/stream" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			close(canceled)
			return
		}
		w.WriteHeader(204)
	}))
	defer up.Close()
	_, cfg := optionalGateway(t, up.URL)
	cfg.Listen = Listen{HTTP: freePort(t)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, DSMPorts{HTTP: 5000, HTTPS: 5001}, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("gateway did not stop")
		}
	})
	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Listen.HTTP)
	get := func(path string) (*http.Response, error) {
		r, _ := http.NewRequest("GET", base+path, nil)
		r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		return client.Do(r)
	}
	for i := 0; ; i++ {
		res, err := get("/dav/file")
		if err == nil {
			res.Body.Close()
			if res.StatusCode != 403 {
				t.Fatalf("default %d", res.StatusCode)
			}
			break
		}
		if i == 100 {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	admin := optionalAPI(t, cfg, true)
	set := func(enabled bool) {
		t.Helper()
		result := optionalCall(t, admin, `{"method":"gateway.passthrough.set","params":{"id":"client","enabled":`+strconv.FormatBool(enabled)+`}}`)
		if result["error"] != nil {
			t.Fatalf("control: %v", result)
		}
	}
	set(true)
	for i := 0; ; i++ {
		res, err := get("/dav/file")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == 204 {
			break
		}
		if i == 100 {
			t.Fatalf("SIGHUP did not enable: %d", res.StatusCode)
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, err := get("/dav/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("stream not started")
	}
	set(false)
	// No reload acknowledgement or generation polling: after the atomic write,
	// the very next request must be denied even if the signal is still pending.
	res, err := get("/dav/file")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("close next request: %d", res.StatusCode)
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("SIGHUP did not cancel upstream stream")
	}
	io.Copy(io.Discard, response.Body)
	pid := filepath.Join(cfg.StateDir, "gateway.pid")
	if _, err := os.Stat(pid); err != nil {
		t.Fatal(err)
	}
}
