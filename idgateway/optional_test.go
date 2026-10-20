package idgateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func optionalGateway(t *testing.T, upstreamURL string) (*Gateway, Config) {
	t.Helper()
	path, _ := writeConfig(t, strings.Replace(paperlessConfig, `"passthrough": ["/share/"],`, `"passthrough": ["/share/"], "optional_passthrough": {"client": {"prefixes": ["/dav"]}},`, 1))
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Upstream = upstreamURL
	cfg.Mode = ModeToken
	cfg.OIDC = nil
	cfg.Token = &TokenConfig{Command: []string{"/unused-adapter"}}
	cfg.Access = "dsm-admins"
	gw, err := Build(cfg, DSMPorts{HTTP: 5000, HTTPS: 5001})
	if err != nil {
		t.Fatal(err)
	}
	return gw, cfg
}

func requestOptional(h http.Handler, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://nas.example:18443"+path, nil)
	r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	r.Header.Set("Cookie", "id=dsm-secret; synopkgland_sso_App=forged")
	r.Header.Set("Remote-User", "administrator")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestOptionalPassthroughDefaultsClosedAndPreservesNativeCredentials(t *testing.T) {
	var auth, cookie, identity string
	hits := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		auth = r.Header.Get("Authorization")
		cookie = r.Header.Get("Cookie")
		identity = r.Header.Get("Remote-User")
		w.WriteHeader(204)
	}))
	defer up.Close()
	gw, cfg := optionalGateway(t, up.URL)
	for _, path := range []string{"/dav", "/dav/file"} {
		if w := requestOptional(gw.Front, "PROPFIND", path); w.Code != 403 || hits != 0 {
			t.Fatalf("default %s: %d hits=%d", path, w.Code, hits)
		}
	}
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "passthrough-client.json"), []byte("true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := requestOptional(gw.Front, "PROPFIND", "/dav/file"); w.Code != 403 {
		t.Fatalf("enable must wait for reload, got %d", w.Code)
	}
	gw.Front.ReloadOptionalPassthrough()
	if w := requestOptional(gw.Front, "PROPFIND", "/dav/file"); w.Code != 204 || auth != "Basic dXNlcjpwYXNz" || cookie != "" || identity != "" {
		t.Fatalf("native auth: status %d headers %q %q %q", w.Code, auth, cookie, identity)
	}
	for _, path := range []string{"/davx", "/api/me"} {
		if w := requestOptional(gw.Front, "GET", path); w.Code != 401 {
			t.Fatalf("gate %s: %d", path, w.Code)
		}
	}
	before := hits
	os.WriteFile(filepath.Join(cfg.StateDir, "passthrough-client.json"), []byte("false\n"), 0600)
	if w := requestOptional(gw.Front, "PROPFIND", "/dav/file"); w.Code != 403 || hits != before {
		t.Fatalf("close before signal must deny: %d hits=%d", w.Code, hits)
	}
}

func TestOptionalPassthroughRejectsMalformedStateAndPathAliases(t *testing.T) {
	up := newUpstream(t, nil)
	gw, cfg := optionalGateway(t, up.URL)
	file := filepath.Join(cfg.StateDir, "passthrough-client.json")
	for _, raw := range []string{"true false", "{}", "null", "\"true\""} {
		os.WriteFile(file, []byte(raw), 0600)
		gw.Front.ReloadOptionalPassthrough()
		if w := requestOptional(gw.Front, "GET", "/dav/x"); w.Code != 403 {
			t.Fatalf("%q: %d", raw, w.Code)
		}
	}
	os.WriteFile(file, []byte("true"), 0600)
	gw.Front.ReloadOptionalPassthrough()
	for _, path := range []string{"/dav/../api/me", "/dav/%2e%2e/api/me", "/dav/%252e%252e/api/me", "/dav%2f..%2fapi/me", "/dav/\\api"} {
		if w := requestOptional(gw.Front, "GET", path); w.Code != 400 {
			t.Fatalf("alias %s: %d", path, w.Code)
		}
	}
	os.Chmod(file, 0644)
	gw.Front.ReloadOptionalPassthrough()
	if w := requestOptional(gw.Front, "GET", "/dav/x"); w.Code != 403 {
		t.Fatalf("unsafe mode: %d", w.Code)
	}
	os.Remove(file)
	target := filepath.Join(t.TempDir(), "enabled")
	os.WriteFile(target, []byte("true"), 0600)
	os.Symlink(target, file)
	gw.Front.ReloadOptionalPassthrough()
	if w := requestOptional(gw.Front, "GET", "/dav/x"); w.Code != 403 {
		t.Fatalf("symlink: %d", w.Code)
	}
}

func TestOptionalPassthroughReloadCancelsInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) }))
	defer up.Close()
	gw, cfg := optionalGateway(t, up.URL)
	file := filepath.Join(cfg.StateDir, "passthrough-client.json")
	os.WriteFile(file, []byte("true"), 0600)
	gw.Front.ReloadOptionalPassthrough()
	done := make(chan struct{})
	go func() { requestOptional(gw.Front, "GET", "/dav/stream"); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("not admitted")
	}
	os.WriteFile(file, []byte("false"), 0600)
	gw.Front.ReloadOptionalPassthrough()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream context not canceled")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy did not finish")
	}
}

// Keep the initial contract probe independent of new Go API names: the
// released decoder must refuse this field until the feature is implemented.
func TestOptionalPassthroughManifestContract(t *testing.T) {
	path, _ := writeConfig(t, strings.Replace(paperlessConfig, `"passthrough": ["/share/"],`, `"optional_passthrough":{"client":{"prefixes":["/dav"]}}, "passthrough": ["/share/"],`, 1))
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	if !strings.Contains(string(raw), "optional_passthrough") {
		t.Fatal("config dropped declaration")
	}
}

func TestOptionalPassthroughRejectsOverlapsAndUnsafeDeclarations(t *testing.T) {
	for _, declaration := range []string{
		`{"client":{"prefixes":["/"]}}`,
		`{"client":{"prefixes":["/_sso/return"]}}`,
		`{"client":{"prefixes":["/dav/%2f"]}}`,
		`{"client":{"prefixes":["/dav//files"]}}`,
		`{"../client":{"prefixes":["/dav"]}}`,
		`{"client":{"prefixes":[]}}`,
		`{"client":{"prefixes":["/dav"],"default_enabled":true}}`,
		`{"client":{"prefixes":["/share/files"]}}`,
		`{"client":{"prefixes":["/dav"]},"other":{"prefixes":["/dav/files"]}}`,
	} {
		path, _ := writeConfig(t, strings.Replace(paperlessConfig, `"passthrough": ["/share/"],`, `"optional_passthrough":`+declaration+`, "passthrough": ["/share/"],`, 1))
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("accepted %s", declaration)
		}
	}
}

func TestOptionalPassthroughDestinationStaysInItsDeclaredRoute(t *testing.T) {
	up := newUpstream(t, nil)
	gw, cfg := optionalGateway(t, up.URL)
	os.WriteFile(optionalStatePath(cfg.StateDir, "client"), []byte("true"), 0600)
	gw.Front.ReloadOptionalPassthrough()
	for _, dest := range []string{"https://elsewhere.example/dav/file", "https://nas.example:18443/api/admin", "/dav/../api/admin", "/dav/%252e%252e/admin", "//elsewhere.example/dav/file"} {
		r := httptest.NewRequest("COPY", "https://nas.example:18443/dav/file", nil)
		r.Header.Set("Destination", dest)
		w := httptest.NewRecorder()
		gw.Front.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("destination %q: %d", dest, w.Code)
		}
	}
}

func TestOptionalPassthroughAcceptsNormalFileNamesAndRetainsSetting(t *testing.T) {
	up := newUpstream(t, nil)
	gw, cfg := optionalGateway(t, up.URL)
	if err := writeOptionalState(cfg.StateDir, "client", true); err != nil {
		t.Fatal(err)
	}
	// Rebuilding the handler (as at process restart) retains the configured bit.
	rebuilt, err := Build(cfg, DSMPorts{HTTP: 5000, HTTPS: 5001})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/dav/100%25.txt", "/dav/a%20b.txt", "/dav/文件.txt"} {
		if w := requestOptional(rebuilt.Front, "GET", path); w.Code != 200 {
			t.Fatalf("filename %s: %d", path, w.Code)
		}
	}
	// A valid DSM admin session must not bypass a closed optional route.
	gw.Front.cfg.Accounts = fakeNames{1: "admin"}
	gw.Front.cfg.Membership = &independentRole{admin: true}
	r := httptest.NewRequest("GET", "https://nas.example:18443/dav/file", nil)
	r.AddCookie(&http.Cookie{Name: gw.Front.cookie, Value: "admin-session"})
	gw.Front.sessions["admin-session"] = session{subject: Subject{UID: 1, Name: "admin", Admin: true}, ip: remoteIP(r), expires: time.Now().Add(time.Hour)}
	subject, valid := gw.Front.session(httptest.NewRecorder(), r)
	if !valid || admissionError(r.Context(), subject, gw.Front.cfg.Access, nil, gw.Front.cfg.Membership) != nil {
		t.Fatal("admin fixture must be a valid admitted session")
	}
	w := httptest.NewRecorder()
	gw.Front.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("admin bypassed disabled route: %d", w.Code)
	}
}
