package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeIngressBindsPrefixToSocketPackage(t *testing.T) {
	t.Setenv("GATEWAY_INTERFACE", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, prefix := range []string{"/synopkgland/NamespaceFixture/", "/old-alias/", "/synopkgland/OtherApp/", "/synopkgland/namespacefixture/", "/synopkgland/%4eamespaceFixture/", "/synopkgland/NamespaceFixture/child/", "/synopkgland/NamespaceFixture", "/synopkgland/NamespaceFixture/?x=1"} {
		t.Setenv(IngressSocketEnv, "/tmp/synopkgland-ingress-NamespaceFixture.sock")
		t.Setenv(IngressPathEnv, prefix)
		err := ServeIngress(ctx, http.NotFoundHandler())
		if want := prefix == "/synopkgland/NamespaceFixture/"; (err == nil) != want {
			t.Errorf("prefix %q: got %v, want accepted=%v", prefix, err, want)
		}
	}
}

// The ingress handler strips the declared path, takes the client address
// from nginx and gives raw HTTP routes the stream deadline.
func TestIngressHandlerPathAddressAndDeadlines(t *testing.T) {
	var seen []string
	srv, err := NewServerWithPermissions(&fakeAuth{user: "alice"}, []byte(httpPolicy),
		HandleHTTP("/proxy/", "app.proxy", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deadline, _ := r.Context().Deadline()
			seen = append(seen, fmt.Sprintf("%s %s %v", r.URL.Path, r.RemoteAddr, time.Until(deadline) > time.Minute))
		})))
	if err != nil {
		t.Fatal(err)
	}
	h := ingressHandler(srv, "/synopkgland/App/", ingressOptions{request: 10 * time.Second, stream: time.Hour})
	for _, tc := range []struct {
		path, addr string
		status     int
	}{
		{"/synopkgland/App/proxy/file", "192.0.2.7", 200},
		{"/synopkgland/App/proxy/file", "::ffff:192.0.2.8", 200},
		{"/synopkgland/App/proxy/file", "", 400},
		{"/synopkgland/App/proxy/file", "fe80::1%eth0", 400},
		{"/other/proxy/file", "192.0.2.7", 404},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://nas.local"+tc.path, nil)
		r.Header.Set("Origin", "http://nas.local")
		if tc.addr != "" {
			r.Header.Set("X-Real-IP", tc.addr)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s from %q: status %d, want %d: %s", tc.path, tc.addr, w.Code, tc.status, w.Body)
		}
	}
	want := []string{"/proxy/file 192.0.2.7:0 true", "/proxy/file 192.0.2.8:0 true"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("handler saw %q, want %q", seen, want)
	}
	// JSON calls keep the request deadline.
	var left time.Duration
	h = ingressHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, _ := r.Context().Deadline()
		left = time.Until(deadline)
	}), "/synopkgland/App/", ingressOptions{request: 10 * time.Second, stream: time.Hour})
	r := httptest.NewRequest(http.MethodPost, "http://nas.local/synopkgland/App/", strings.NewReader("{}"))
	r.Header.Set("X-Real-IP", "192.0.2.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if left <= 0 || left > 10*time.Second {
		t.Fatalf("JSON deadline %v, want at most 10s", left)
	}
}

func TestServeIngressRefusesCGIAndForeignSockets(t *testing.T) {
	ok := http.NotFoundHandler()
	t.Setenv(IngressSocketEnv, "/tmp/synopkgland-ingress-App.sock")
	t.Setenv(IngressPathEnv, "/synopkgland/App/")
	t.Setenv("GATEWAY_INTERFACE", "CGI/1.1")
	if err := ServeIngress(context.Background(), ok); err == nil {
		t.Fatal("served under CGI")
	}
	os.Unsetenv("GATEWAY_INTERFACE")
	for _, env := range [][2]string{{"/tmp/other.sock", "/synopkgland/App/"}, {"/tmp/synopkgland-ingress-App.sock", "/App-API/"}, {"/tmp/synopkgland-ingress-App.sock", "app-api"}} {
		t.Setenv(IngressSocketEnv, env[0])
		t.Setenv(IngressPathEnv, env[1])
		if err := ServeIngress(context.Background(), ok); err == nil {
			t.Fatalf("accepted %v", env)
		}
	}
	file := filepath.Join(t.TempDir(), "plain")
	os.WriteFile(file, nil, 0o600)
	if err := serveIngress(context.Background(), file, ok); err == nil {
		t.Fatal("replaced a regular file")
	}
}

// The socket serves until cancel, replaces a stale socket and is removed after.
func TestServeIngressSocketLifecycle(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ingress")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "s.sock")
	stale, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveIngress(ctx, socket, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	}()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	var body []byte
	for i := 0; i < 100; i++ {
		if res, err := client.Get("http://nas.local/"); err == nil {
			body, _ = io.ReadAll(res.Body)
			res.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if string(body) != "ok" {
		t.Fatalf("served %q", body)
	}
	if st, err := os.Stat(socket); err != nil || st.Mode().Perm() != 0o666 {
		t.Fatalf("socket mode: %v %v", st, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatal("socket left behind")
	}
}
