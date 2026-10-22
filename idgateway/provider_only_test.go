package idgateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func TestProviderOnlyNativeWebSocketWithoutOriginUsesUpstreamAuthentication(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer native" {
			t.Error("App bearer lost")
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		rw.Flush()
		frame := make([]byte, 8)
		if _, err = io.ReadFull(rw, frame); err != nil {
			t.Error(err)
			return
		}
		for i := 0; i < 2; i++ {
			frame[6+i] ^= frame[2+i%4]
		}
		rw.Write([]byte{0x81, 2, frame[6], frame[7]})
		rw.Flush()
	})
	g := providerOnlyFront(t, up)
	srv := httptest.NewServer(g.Front)
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(conn, "GET /socket HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer native\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", addr)
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 101 {
		t.Fatalf("native upgrade: %d", res.StatusCode)
	}
	conn.Write([]byte{0x81, 0x82, 1, 2, 3, 4, 'h' ^ 1, 'i' ^ 2})
	frame := make([]byte, 4)
	if _, err = io.ReadFull(br, frame); err != nil || !bytes.Equal(frame, []byte{0x81, 2, 'h', 'i'}) {
		t.Fatalf("native WebSocket echo: %v %v", frame, err)
	}
}

func providerOnlyConfig(t *testing.T, edits map[string]any) (Config, error) {
	t.Helper()
	raw := map[string]any{"package": "App", "mode": "oidc", "access": "dsm-users", "upstream_access": "public",
		"upstream": "http://127.0.0.1:8000", "listen": map[string]int{"https": 18443},
		"state_dir": t.TempDir(), "authorize_cgi": "/webman/3rdparty/App/sso/authorize.cgi",
		"oidc": map[string]any{"issuer_port": 18090, "clients": []any{map[string]any{"id": "reader", "redirect_paths": []string{"/cb"}, "require_pkce": true}}}}
	for k, v := range edits {
		raw[k] = v
	}
	data, _ := json.Marshal(raw)
	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(path)
}

func providerOnlyFront(t *testing.T, up *upstream) *Gateway {
	t.Helper()
	cfg, err := providerOnlyConfig(t, map[string]any{"upstream": up.URL})
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(cfg, DSMPorts{HTTP: 5000, HTTPS: 5001})
	if err != nil {
		t.Fatal(err)
	}
	g.Front.cfg.Now = func() time.Time { return cgiNow }
	g.Provider.now = g.Front.cfg.Now
	g.Front.cfg.Key = testKey
	g.Front.cfg.Accounts = fakeNames{1026: "alice", 1027: "bob"}
	return g
}

func TestProviderOnlyNativeRoutesDoNotRequireDSMOrBrowserOrigin(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer app-token" {
			t.Error("native App credential lost")
		}
		if strings.Contains(r.Header.Get("Cookie"), "id=") || r.Header.Get("X-Syno-User") != "" {
			t.Error("DSM identity leaked upstream")
		}
		w.WriteHeader(418)
	})
	g := providerOnlyFront(t, up)
	for _, origin := range []string{"", "https://foreign.example"} {
		b := newBrowser(t, g.Front)
		b.cookies["id"], b.cookies["app-session"] = "private-dsm-cookie", "native-session"
		for _, method := range []string{"GET", "POST", "DELETE"} {
			w := b.do(method, "https://nas.example:18443/identity/connect/token", "Authorization", "Bearer app-token", "Origin", origin, "X-Syno-User", "forged")
			if w.Code != 418 {
				t.Fatalf("%s native request got %d; upstream must own admission", method, w.Code)
			}
		}
	}
	g.Front.cfg.Ready = func(context.Context) bool { return false }
	if w := newBrowser(t, g.Front).do("GET", "https://nas.example:18443/"); w.Code != 503 {
		t.Fatalf("readiness bypassed: %d", w.Code)
	}
}

func TestProviderOnlyIssuerStillNeedsVerifiedDSMAndAppAdmission(t *testing.T) {
	for _, reason := range []error{api.ErrNoSession, api.ErrAppPrivilegeDenied, api.ErrAdminRequired, errors.New("unavailable")} {
		t.Run(reason.Error(), func(t *testing.T) {
			g := providerOnlyFront(t, newUpstream(t, nil))
			b := newBrowser(t, g.Front)
			start := b.do("GET", origin+"/_sso/authorize?"+authQuery("reader", origin+"/cb", "v").Encode(), navigate...)
			w := finishCGI(t, b, start, verifierFunc(func(*http.Request) (api.Identity, error) { return api.Identity{}, reason }))
			if w.Code != 401 && w.Code != 403 && w.Code != 503 {
				t.Fatalf("denial: %d", w.Code)
			}
			if len(g.Provider.codes) != 0 || len(g.Provider.tokens) != 0 {
				t.Fatal("denied identity obtained a grant")
			}
			form := url.Values{"grant_type": {"authorization_code"}, "code": {"forged"}, "redirect_uri": {origin + "/cb"}, "client_id": {"reader"}, "client_secret": {g.Secrets["reader"]}, "code_verifier": {"v"}}
			if token := exchange(g.Provider, form); token.Code != 400 {
				t.Fatalf("forged code: %d", token.Code)
			}
		})
	}
	g := providerOnlyFront(t, newUpstream(t, nil))
	b := newBrowser(t, g.Front)
	w := b.do("POST", origin+ReturnPath, "Origin", "https://foreign.example")
	if w.Code != 403 {
		t.Fatalf("reserved path lost origin checks: %d", w.Code)
	}
	if w = b.do("GET", origin+"/_sso/token"); w.Code != 404 {
		t.Fatalf("loopback token endpoint exposed: %d", w.Code)
	}
	// A signed assertion is not enough after administrator membership changes.
	g.Front.cfg.Access = "dsm-admins"
	g.Front.cfg.Membership = &changingMembership{groups: []string{"users"}}
	start := b.do("GET", origin+"/_sso/authorize?"+authQuery("reader", origin+"/cb", "v").Encode(), navigate...)
	w = finishCGI(t, b, start, verifierFunc(func(*http.Request) (api.Identity, error) { return api.Identity{User: "alice", Admin: true}, nil }))
	if w.Code != 403 || len(g.Provider.codes) != 0 {
		t.Fatal("issuer accepted stale administrator claim")
	}
}

func TestProviderOnlyVerifiedGrantRetainsPKCEAndSingleUse(t *testing.T) {
	g := providerOnlyFront(t, newUpstream(t, nil))
	b := newBrowser(t, g.Front)
	start := b.do("GET", origin+"/_sso/authorize?"+authQuery("reader", origin+"/cb", "v").Encode(), navigate...)
	w := finishCGI(t, b, start, verifierFunc(func(*http.Request) (api.Identity, error) { return api.Identity{User: "alice"}, nil }))
	if w.Code != 302 {
		t.Fatalf("verified callback: %d %s", w.Code, w.Body)
	}
	code := codeFrom(t, w.Header().Get("Location"))
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {origin + "/cb"}, "client_id": {"reader"}, "client_secret": {g.Secrets["reader"]}, "code_verifier": {"v"}}
	if w = exchange(g.Provider, form); w.Code != 200 {
		t.Fatalf("verified token: %d %s", w.Code, w.Body)
	}
	if w = exchange(g.Provider, form); w.Code != 400 {
		t.Fatalf("code replay: %d", w.Code)
	}
}

func TestProviderOnlyConfigurationFailsClosed(t *testing.T) {
	for _, edits := range []map[string]any{
		{"mode": "header"}, {"access": "public"}, {"access": ""}, {"upstream_access": "publci"},
		{"compose_blocked": true}, {"optional_passthrough": map[string]any{"mobile": map[string]any{"prefixes": []string{"/api"}}}},
	} {
		if cfg, err := providerOnlyConfig(t, edits); err == nil {
			if _, err = Build(cfg, DSMPorts{HTTPS: 5001}); err == nil {
				t.Fatalf("unsafe configuration accepted: %v", edits)
			}
		}
	}
}
