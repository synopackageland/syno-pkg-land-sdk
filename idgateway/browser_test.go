package idgateway

import (
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
)

func TestOIDCCallbackRetainsAdministratorAdmission(t *testing.T) {
	membership := &changingMembership{groups: []string{"administrators"}}
	f, c := newFront(t, ModeOIDC, newUpstream(t, nil), func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = membership })
	b := newBrowser(t, f)
	callback := "https://nas.example:18443/accounts/oidc/dsm/login/callback/"
	authorize := "https://nas.example:18443/_sso/authorize?" + authQuery("paperless", callback, "v").Encode()
	w := b.do("GET", dsmBounce(t, b.do("GET", authorize), alice, c.t))
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Query().Get("code") == "" {
		t.Fatalf("no code: %d", w.Code)
	}
	if w = b.do("GET", callback+"?code=example", navigate...); w.Code != 200 {
		t.Fatalf("callback lost gateway admission: %d %s", w.Code, w.Header().Get("Location"))
	}
	membership.groups = []string{"users"}
	if w = b.do("GET", callback, navigate...); w.Code != http.StatusForbidden {
		t.Fatalf("revoked administrator: %d", w.Code)
	}
}

func TestBrowserBridgeOnlyChangesNavigatedHTML(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if isNavigation(r) && r.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("bridge must request identity encoding")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", `"original"`)
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'")
		w.Write([]byte("<!doctype html><html><head><script>upstream()</script></head></html>"))
	})
	f, _ := newFront(t, ModeOIDC, up, func(c *FrontConfig) {
		c.Browser = &BrowserConfig{Start: "/auth/oidc?method=login", LoginPaths: []string{"/login"}, CallbackPaths: []string{"/callback"}, CompletionQuery: "token"}
	})
	b := newBrowser(t, f)
	w := b.do("GET", "https://nas.example:18443/deep", append(navigate, "If-None-Match", `"old"`)...)
	if up.last.Header.Get("If-None-Match") != "" {
		t.Fatal("conditional response could skip bridge")
	}

	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `<head><script src="/_sso/browser.js"></script><script>upstream()`) {
		t.Fatalf("bridge not early: %d %s", w.Code, body)
	}
	if w.Header().Get("ETag") != "" || w.Header().Get("Content-Security-Policy") != "default-src 'self'; script-src 'self'" {
		t.Fatal("stale validator or changed policy")
	}
	api := b.do("GET", "https://nas.example:18443/api/document", "Accept", "application/json")
	if strings.Contains(api.Body.String(), "/_sso/browser.js") {
		t.Fatal("rewrote a non-navigation response")
	}
	w = b.do("GET", "https://nas.example:18443/_sso/browser.js")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/auth/oidc?method=login") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing configured script")
	}
}

func TestBrowserConfigRejectsUnsafePaths(t *testing.T) {
	for _, start := range []string{"https://evil.example/", "//evil.example/", "/a/../b", "/a%2f..%2fb", "/_sso/authorize", "/x\\y", "/auth#x"} {
		if err := (BrowserConfig{Start: start, LoginPaths: []string{"/login"}, CallbackPaths: []string{"/callback"}}).validate(); err == nil {
			t.Errorf("accepted %q", start)
		}
	}
}

func TestOIDCColdDSMLoginUsesDeclaredAppResume(t *testing.T) {
	h := newCGI(&fakeDSM{})
	h.LoginApp = "App"
	request := httptest.NewRequest("POST", "https://nas.example:5001/webman/3rdparty/App/sso/authorize.cgi?txn=opaque&return=https%3A%2F%2Fnas.example%3A18443%2F_sso%2Fdsm-return", nil)
	// No session: the real DSM login keeps a bounded transaction, not an arbitrary return URL.
	h.Verifier = verifierFunc(func(*http.Request) (api.Identity, error) { return api.Identity{}, api.ErrNoSession })
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Path != "/" || loc.Query().Get("launchApp") != "SYNO.SDS.App.Instance" || !strings.Contains(loc.Query().Get("launchParam"), `"ssoResume":`) {
		t.Fatalf("missing login resume: %s", loc)
	}
	request.URL.RawQuery += "&login_attempt=1"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if !strings.Contains(w.Header().Get("Location"), "error=login_required") {
		t.Fatal("repeated login loop")
	}
}

func TestBrowserScriptNavigationScenarios(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("browser script tests require Node, provided by the SDK gate")
	}
	cmd := exec.Command(node, "--test", "browser.test.mjs")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser behavior: %v\n%s", err, out)
	}
}
