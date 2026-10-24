package idgateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type menuTokenAuth struct{ calls int }

func (a *menuTokenAuth) Authenticate(r *http.Request) (api.Identity, error) {
	a.calls++
	if r.Header.Get("X-SYNO-TOKEN") != "valid-token" {
		return api.Identity{}, api.ErrNoSession
	}
	return api.Identity{User: "administrator", Admin: true}, nil
}

func TestMenuSessionBootstrap(t *testing.T) {
	cfg, src := menuInputs()
	auth := &menuTokenAuth{}
	h := entryHandlerWithMenu(cfg, nil, nil, auth, func() MenuSources { return src }, func() bool { return true })
	request := func(method, rawURL, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, rawURL, strings.NewReader(body))
		r.Header.Set("Cookie", "id=dsm-session")
		if method == http.MethodPost {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// DSM's URL menu opens a plain GET: its session cookie does not supply
	// the token authenticate.cgi needs. The first response must only bootstrap.
	w := request("GET", "https://dsm.example.com/open.cgi", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "enable_syno_token=yes") || !strings.Contains(w.Body.String(), `method="post"`) || auth.calls != 0 {
		t.Fatalf("plain menu GET must bootstrap, status=%d calls=%d body=%s", w.Code, auth.calls, w.Body)
	}
	for _, secret := range []string{"app.example.com", "nas.example.ts.net", "launchApp=", "Cloudflare"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("bootstrap exposes protected details: %s", secret)
		}
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self'") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}

	// A same-origin form carries the token into the existing session authority.
	w = request("POST", "https://dsm.example.com/open.cgi", "SynoToken=valid-token", "https://dsm.example.com")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-ping="https://app.example.com/_spl/ping"`) || auth.calls != 1 {
		t.Fatalf("authenticated form did not reach the probe: %d %s", w.Code, w.Body)
	}

	for _, tc := range []struct{ body, origin, path string }{
		{"SynoToken=valid-token", "https://other.example", "/open.cgi"},
		{"", "https://dsm.example.com", "/open.cgi"},
		{"SynoToken=valid-token&SynoToken=valid-token", "https://dsm.example.com", "/open.cgi"},
		{"SynoToken=valid-token&destination=https://other.example", "https://dsm.example.com", "/open.cgi"},
		{"SynoToken=valid-token", "https://dsm.example.com", "/open.cgi?ssoResume=anything"},
		{"SynoToken=" + strings.Repeat("x", 9<<10), "https://dsm.example.com", "/open.cgi"},
	} {
		before := auth.calls
		w = request("POST", "https://dsm.example.com"+tc.path, tc.body, tc.origin)
		if w.Code < 400 || auth.calls != before || strings.Contains(w.Body.String(), "app.example.com") {
			t.Fatalf("unsafe form accepted: %+v status=%d calls=%d", tc, w.Code, auth.calls)
		}
	}
	// A valid-shaped but expired token must still be refused by authenticate.cgi.
	w = request("POST", "https://dsm.example.com/open.cgi", url.Values{"SynoToken": {"expired"}}.Encode(), "https://dsm.example.com")
	if !strings.Contains(w.Body.String(), `data-error-code="sign_in_required"`) || strings.Contains(w.Body.String(), "app.example.com") {
		t.Fatal(w.Body)
	}

	// LAN never needs the new handshake, even when a DSM cookie is present.
	before := auth.calls
	w = request("GET", "https://nas.local:5001/open.cgi", "", "")
	if w.Code != 302 || w.Header().Get("Location") != "https://nas.local:18443/" || auth.calls != before || strings.Contains(w.Body.String(), "enable_syno_token") {
		t.Fatal(w.Code, w.Header(), w.Body)
	}
	w = request("POST", "https://nas.local:5001/open.cgi", "SynoToken=valid-token", "https://nas.local:5001")
	if w.Code < 400 || auth.calls != before {
		t.Fatal("new POST must not change the LAN launcher", w.Code)
	}
}

func TestMenuBootstrapLocales(t *testing.T) {
	cfg, src := menuInputs()
	for _, locale := range denialLocales {
		auth := &menuTokenAuth{}
		r := httptest.NewRequest("GET", "https://dsm.example.com/open.cgi", nil)
		r.Header.Set("Cookie", "id=dsm-session")
		r.Header.Set("Accept-Language", locale)
		w := httptest.NewRecorder()
		entryHandlerWithMenu(cfg, nil, nil, auth, func() MenuSources { return src }, func() bool { return true }).ServeHTTP(w, r)
		if w.Header().Get("Content-Language") != locale || !strings.Contains(w.Body.String(), `action="?"`) || strings.Contains(w.Body.String(), "app.example.com") || auth.calls != 0 {
			t.Fatal(locale, w.Header(), w.Body)
		}
	}
}
