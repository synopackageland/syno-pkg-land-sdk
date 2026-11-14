package idgateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const unixTestSocket = "/var/packages/App/var/run/app.sock"

// unixUpstream serves h on a temp socket and routes the gateway's seam to it,
// checking the package and path the gateway asks the IPC dialer for.
func unixUpstream(t *testing.T, h http.Handler) func(*FrontConfig) {
	t.Helper()
	l, err := net.Listen("unix", filepath.Join(t.TempDir(), "s"))
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return func(cfg *FrontConfig) {
		cfg.Upstream = "unix:" + unixTestSocket
		cfg.dialUnix = func(ctx context.Context, pkg, path string) (net.Conn, error) {
			if pkg != "App" || path != unixTestSocket {
				t.Errorf("dialed %q %q", pkg, path)
			}
			return (&net.Dialer{}).DialContext(ctx, "unix", l.Addr().String())
		}
	}
}

func TestUnixUpstreamProxiesAndKeepsAdmission(t *testing.T) {
	var hits int
	edit := unixUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		fmt.Fprintf(w, "host=%s path=%s", r.Host, r.URL.Path)
	}))
	f, _ := newFront(t, ModeHeader, newUpstream(t, nil), func(cfg *FrontConfig) {
		edit(cfg)
		cfg.Entry = "admin-only"
		cfg.Passthrough = nil
		cfg.UpstreamAuthPaths = []string{"/reverse"}
	})
	b := newBrowser(t, f)
	rec := b.do("GET", "http://nas.example/reverse/x")
	if rec.Code != 200 || rec.Body.String() != "host=nas.example path=/reverse/x" {
		t.Fatalf("upstream auth path: %d %q", rec.Code, rec.Body.String())
	}
	before := hits
	if rec := b.do("GET", "http://nas.example/other"); rec.Code == 200 || hits != before {
		t.Fatalf("non-upstream path reached the unix upstream without admission: %d", rec.Code)
	}
}

func TestUnixUpstreamWebSocketIsNotCutByGatewayTimeouts(t *testing.T) {
	const short = 200 * time.Millisecond
	edit := unixUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		rw.Flush()
		io.Copy(conn, rw)
	}))
	f, _ := newFront(t, ModeHeader, newUpstream(t, nil), func(cfg *FrontConfig) {
		edit(cfg)
		cfg.Entry = "admin-only"
		cfg.Passthrough = nil
		cfg.UpstreamAuthPaths = []string{"/reverse"}
		cfg.ResponseHeaderTimeout = short
	})
	// The public server uses the same kinds of limits as run.go
	// (ReadHeaderTimeout 30s, IdleTimeout 2m), shrunk so the test takes seconds.
	srv := httptest.NewUnstartedServer(f)
	srv.Config.ReadHeaderTimeout, srv.Config.IdleTimeout = short, short
	srv.Start()
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(conn, "GET /reverse HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", addr)
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, nil)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade denied: %v %v", res, err)
	}
	for i := 0; i < 2; i++ {
		time.Sleep(6 * short) // idle well past every limit
		if _, err := conn.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "ping" {
			t.Fatalf("idle upgraded stream was cut (round %d): %q %v", i, buf, err)
		}
	}
}

func TestUnixUpstreamConfigValidation(t *testing.T) {
	dial := func(context.Context, string, string) (net.Conn, error) { return nil, io.EOF }
	long := "unix:/var/packages/App/var/run/" + strings.Repeat("a", 60) + ".sock"
	for _, tc := range []struct {
		upstream string
		compose  bool
		ok       bool
	}{
		{"unix:" + unixTestSocket, false, true},
		{"unix:/var/packages/Other/var/run/app.sock", false, false},
		{"unix:/var/packages/App/var/run/sub/app.sock", false, false},
		{"unix:/var/packages/App/var/run/../run/app.sock", false, false},
		{"unix:/var/packages/App/var/run/../../x/app.sock", false, false},
		{"unix:var/packages/App/var/run/app.sock", false, false},
		{"unix:app.sock", false, false},
		{"unix:/var/packages/App/var/run/app", false, false},
		{"unix:/var/packages/App/var/run/app.sock/", false, false},
		{"unix:/var/packages/App/var/run//app.sock", false, false},
		{"unix:/var/packages/App/var/log/app.sock", false, false},
		{"unix:/var/packages/App/var/run/.sock.x", false, false},
		{long, false, false},
		{"unix:", false, false},
		{"unix:" + unixTestSocket, true, false},
	} {
		_, err := NewFront(FrontConfig{
			Mode: ModeHeader, Upstream: tc.upstream, dialUnix: dial, ComposeIngress: composeFor(tc.compose),
			Key: testKey, AuthorizeCGI: "/webman/3rdparty/App/sso/authorize.cgi", DSMPorts: DSMPorts{HTTP: 5000, HTTPS: 5001},
			Package: "App", Headers: HeaderNames{User: "Remote-User"}, Roles: Roles{Admin: "admin"}, Accounts: fakeNames{1026: "alice"},
		})
		if (err == nil) != tc.ok {
			t.Errorf("%q compose=%v: err=%v want ok=%v", tc.upstream, tc.compose, err, tc.ok)
		}
		cerr := (Config{Package: "App", Upstream: tc.upstream, ComposeIngress: composeFor(tc.compose)}).validate()
		if tc.upstream != "" && !tc.ok && cerr == nil {
			t.Errorf("Config.validate accepted %q compose=%v", tc.upstream, tc.compose)
		}
	}
}

func composeFor(on bool) *ComposeIngress {
	if on {
		return &ComposeIngress{Candidate: 1}
	}
	return nil
}
