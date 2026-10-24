package idgateway

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

var document = append([]string{"Sec-Fetch-Dest", "document"}, navigate...)

func singleOriginIngress(t *testing.T, single bool, external func() External) *browser {
	t.Helper()
	f, _ := newFront(t, ModeHeader, newUpstream(t, nil), func(c *FrontConfig) {
		c.External = external
		c.SingleOrigin = single
	})
	ingress := nativeIngressHandler(Config{Package: "App", Listen: Listen{HTTPS: 18443}}, f)
	return newBrowser(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
		r.Header.Set(nativeClientIPHeader, "192.0.2.7")
		ingress.ServeHTTP(w, r)
	}))
}

func TestSingleOriginRedirectsOtherHostNavigations(t *testing.T) {
	e := External{Accept: []string{"https://vault.example.com", "https://connect.example.com"}, Primary: "https://vault.example.com"}
	b := singleOriginIngress(t, true, func() External { return e })
	for _, origin := range []string{"https://nas.example.com:18443", "https://192.0.2.10:18443"} {
		w := b.do("GET", origin+"/admin/x?a=1", document...)
		if w.Code != 302 || w.Header().Get("Location") != "https://vault.example.com/admin/x?a=1" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(origin, w.Code, w.Header())
		}
	}
	for name, req := range map[string][]string{
		"canonical": append([]string{"GET", "https://vault.example.com/"}, document...),
		"public":    append([]string{"GET", "https://connect.example.com/"}, document...),
		"api":       {"GET", "https://nas.example.com:18443/api/config", "Sec-Fetch-Mode", "cors", "Sec-Fetch-Dest", "empty"},
		"native":    {"GET", "https://nas.example.com:18443/api/config"},
		"frame":     {"GET", "https://nas.example.com:18443/", "Sec-Fetch-Mode", "navigate", "Sec-Fetch-Dest", "iframe"},
		"post":      append([]string{"POST", "https://nas.example.com:18443/", "Origin", "https://nas.example.com:18443"}, document...),
		"sso":       append([]string{"GET", "https://nas.example.com:18443/_sso/return"}, document...),
		"ping":      append([]string{"GET", "https://nas.example.com:18443" + PingPath}, document...),
	} {
		w := b.do(req[0], req[1], req[2:]...)
		if strings.HasPrefix(w.Header().Get("Location"), "https://vault.example.com") && !strings.Contains(w.Header().Get("Location"), "/_sso/") {
			t.Fatal(name, w.Code, w.Header())
		}
	}
}

func TestSingleOriginKeepsLocalHostsWithoutExternalOrigin(t *testing.T) {
	for _, tc := range []struct {
		single bool
		e      External
	}{{true, External{Primary: "https://install.example.com:29443"}}, {false, External{Accept: []string{"https://vault.example.com"}, Primary: "https://vault.example.com"}}} {
		b := singleOriginIngress(t, tc.single, func() External { return tc.e })
		w := b.do("GET", "https://nas.example.com:18443/", document...)
		if strings.HasPrefix(w.Header().Get("Location"), "https://vault.example.com") || strings.HasPrefix(w.Header().Get("Location"), "https://install.example.com") {
			t.Fatal(tc.single, w.Code, w.Header())
		}
	}
}

func TestSingleOriginLauncherOpensCanonicalOrigin(t *testing.T) {
	cfg := Config{Package: "App", Mode: ModeOIDC, NativeIngress: true, Listen: Listen{HTTPS: 18443}, AuthorizeCGI: "/authorize.cgi", OIDC: &OIDCConfig{NativeLogin: true}, LaunchPath: "/#/sso?identifier=x", External: &ExternalConfig{SingleOrigin: true}}
	e := External{Accept: []string{"https://vault.example.com"}, Primary: "https://vault.example.com"}
	open := func(cfg Config, e External, query string) string {
		w := httptest.NewRecorder()
		entryHandlerWithExternal(cfg, nil, nil, nil, func() External { return e }).ServeHTTP(w, httptest.NewRequest("GET", "https://nas.example.com:5001/open.cgi"+query, nil))
		if w.Code != 302 {
			t.Fatal(w.Code, w.Body)
		}
		return w.Header().Get("Location")
	}
	// The canonical origin opens after the launcher page checks it answers.
	w := httptest.NewRecorder()
	signedIn := verifierFunc(func(*http.Request) (api.Identity, error) { return api.Identity{User: "alice"}, nil })
	entryHandlerWithMenu(cfg, nil, nil, signedIn, func() MenuSources { return MenuSources{External: e} }, func() bool { return false }).ServeHTTP(w, httptest.NewRequest("GET", "https://nas.example.com:5001/open.cgi", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-target="https://vault.example.com/#/sso?identifier=x"`) || !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src https://vault.example.com;") {
		t.Fatal(w.Code, w.Header(), w.Body)
	}
	if got := open(cfg, External{}, ""); got != "https://nas.example.com:18443/#/sso?identifier=x" {
		t.Fatal("no external origin", got)
	}
	plain := cfg
	plain.External = &ExternalConfig{}
	if got := open(plain, e, ""); got != "https://nas.example.com:18443/#/sso?identifier=x" {
		t.Fatal("multi-origin App", got)
	}
	// A local resume returns to the host whose transaction cookie started it.
	got, _ := url.Parse(open(cfg, e, "?ssoResume="+url.QueryEscape("txn:18443")))
	if got.Query().Get("return") != "https://nas.example.com:18443"+ReturnPath {
		t.Fatal("resume", got)
	}
}

func TestSingleOriginConfigRoundTrip(t *testing.T) {
	var c ExternalConfig
	if err := json.Unmarshal([]byte(`{"connect":"optional","origin_change":"confirm","single_origin":true}`), &c); err != nil || !c.SingleOrigin || c.validate() != nil {
		t.Fatal(c, err)
	}
	e := External{Accept: []string{"https://vault.example.com"}, Primary: "https://vault.example.com"}
	if e.Canonical(&c) != "https://vault.example.com" || e.Canonical(&ExternalConfig{}) != "" || e.Canonical(nil) != "" || (External{Primary: "https://x.example.com:29443"}).Canonical(&c) != "" {
		t.Fatal("canonical")
	}
}
