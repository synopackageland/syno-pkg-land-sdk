package idgateway

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestExternalColdNavigationReturnsToOriginalURLAndRetryDoesNotLoop(t *testing.T) {
	for _, mode := range []Mode{ModeHeader, ModeNone, ModeToken, ModeOIDC} {
		t.Run(string(mode), func(t *testing.T) {
			e := External{Accept: []string{"https://app.example.com"}, DSM: "https://dsm.example.com", R1: 2, R2: 7}
			f, _ := newFront(t, mode, newUpstream(t, nil), func(c *FrontConfig) {
				c.External = func() External { return e }
				c.Access = "dsm-users"
				c.Tokens = &staticTokens{}
			})
			b := newBrowser(t, f)
			target := "/library/albums?sort=name&filter=a%26b"
			if mode == ModeOIDC {
				target = "/_sso/authorize?" + authQuery("paperless", e.Accept[0]+"/accounts/oidc/dsm/login/callback/", "v").Encode()
			}
			start := b.do("GET", e.Accept[0]+target, navigate...)
			authorize, _ := url.Parse(start.Header().Get("Location"))
			if start.Code != 302 || authorize.Scheme+"://"+authorize.Host != e.DSM || authorize.Query().Get("login_attempt") != "" {
				t.Fatalf("first hop: %d %s", start.Code, authorize)
			}
			cgi := newCGI(&fakeDSM{})
			cgi.External = func() External { return e }
			cgi.LoginApp = "App"
			launch := cgiPost(t, cgi, "?"+authorize.RawQuery, "")
			launcher, _ := url.Parse(launch.Header().Get("Location"))
			if launch.Code != 302 || launcher.Path != "/" || launcher.Query().Get("launchApp") != "SYNO.SDS.App.Instance" {
				t.Fatalf("cold login must go to DSM: %d %s", launch.Code, launcher)
			}
			cfg := Config{Package: "App", Mode: mode, Listen: Listen{HTTPS: 18443}, AuthorizeCGI: f.cfg.AuthorizeCGI}
			entry := entryHandlerWithExternal(cfg, nil, nil, nil, func() External { return e })
			resume := func(launcher *url.URL) *url.URL {
				t.Helper()
				var param map[string]string
				if json.Unmarshal([]byte(launcher.Query().Get("launchParam")), &param) != nil || param["ssoResume"] == "" {
					t.Fatal("missing resume", launcher)
				}
				w := httptest.NewRecorder()
				entry.ServeHTTP(w, httptest.NewRequest("GET", e.DSM+"/open.cgi?launchParam="+url.QueryEscape(launcher.Query().Get("launchParam")), nil))
				back, _ := url.Parse(w.Header().Get("Location"))
				if w.Code != 302 || back.Query().Get("login_attempt") != "1" {
					t.Fatalf("resume: %d %s", w.Code, back)
				}
				return back
			}
			// A cancelled or unsuccessful login gets one terminal page, never an
			// automatic second launcher redirect.
			back := resume(launcher)
			failed := cgiPost(t, cgi, "?"+back.RawQuery, "")
			dead := b.do("GET", failed.Header().Get("Location"), navigate...)
			if dead.Code != 401 || dead.Header().Get("Location") != "" || !strings.Contains(dead.Body.String(), `class="primary"`) || !strings.Contains(dead.Body.String(), "<style>") {
				t.Fatalf("second failure must be styled: %d %s", dead.Code, dead.Body)
			}
			if !strings.Contains(dead.Header().Get("Content-Security-Policy"), cspHash(denialStyle)) {
				t.Fatal("styles are not CSP-pinned")
			}
			match := regexp.MustCompile(`<a[^>]*data-i18n="gateway.sign_in"[^>]*href="([^"]+)"`).FindStringSubmatch(dead.Body.String())
			if len(match) != 2 {
				t.Fatal("missing sign-in action", dead.Body)
			}
			retry, _ := url.Parse(html.UnescapeString(match[1]))
			if retry.Scheme+"://"+retry.Host != e.DSM || retry.Query().Get("launchApp") == "" {
				t.Fatal("retry does not sign in and return", retry)
			}
			back = resume(retry)
			if back.Query().Get("txn") == authorize.Query().Get("txn") {
				t.Fatal("retry reused consumed transaction")
			}
			cgi.Verifier = &fakeDSM{user: "alice", token: "tok", addr: "192.0.2.7"}
			done := cgiPost(t, cgi, "?"+back.RawQuery, "tok")
			final := b.do("GET", done.Header().Get("Location"), navigate...)
			if final.Code != 302 {
				t.Fatal(final.Code, final.Body)
			}
			if mode != ModeOIDC && final.Header().Get("Location") != target {
				t.Fatal("original path/query lost", final.Header())
			}
			if mode == ModeOIDC && !strings.HasPrefix(final.Header().Get("Location"), e.Accept[0]+"/accounts/oidc/dsm/login/callback/") {
				t.Fatal("OIDC return lost", final.Header())
			}
			sessions := len(f.sessions)
			if replay := b.do("GET", done.Header().Get("Location"), navigate...); replay.Code != 302 || !strings.HasPrefix(replay.Header().Get("Location"), e.DSM+f.cfg.AuthorizeCGI+"?") || len(f.sessions) != sessions {
				t.Fatal("assertion replay must start a fresh transaction without creating a session", replay.Code)
			}
		})
	}
}

func TestExternalSignedOutAPIStaysJSON(t *testing.T) {
	e := External{Accept: []string{"https://app.example.com"}, DSM: "https://dsm.example.com"}
	f, _ := newFront(t, ModeHeader, newUpstream(t, nil), func(c *FrontConfig) { c.External = func() External { return e } })
	for _, headers := range [][]string{{"Accept", "application/json"}, {"Sec-Fetch-Mode", "cors", "Accept", "text/html"}} {
		w := newBrowser(t, f).do(http.MethodGet, e.Accept[0]+"/api/items", headers...)
		if w.Code != 401 || w.Header().Get("Location") != "" || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatal(w.Code, w.Header(), w.Body)
		}
	}
}

func TestDenialPageLocalesAndCSP(t *testing.T) {
	for _, locale := range denialLocales {
		for _, code := range []string{"login_required", "access_denied", "app_privilege_denied", "temporarily_unavailable"} {
			f, _ := newFront(t, ModeHeader, newUpstream(t, nil), nil)
			f.brand = appBrand{name: `Reader <script>bad</script>`}
			r := httptest.NewRequest("GET", "https://nas.example:18443/", nil)
			r.Header.Set("Accept-Language", locale)
			w := httptest.NewRecorder()
			f.writeFailure(w, r, code, "/library?q=1", true)
			_, messages := denialMessages(r)
			for _, key := range []string{"sign_in_required", "admin_required", "group_required", "app_privilege_denied", "verification_failed", "open_dsm"} {
				if messages[key] == "" || strings.ContainsAny(string([]rune(messages[key])[len([]rune(messages[key]))-1]), ".。．") {
					t.Fatal(locale, key, messages[key])
				}
			}
			if w.Header().Get("Content-Language") != locale || strings.Count(w.Body.String(), `class="primary"`) != 1 || strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "Reader &lt;script&gt;") {
				t.Fatal(locale, code, w.Body)
			}
			if strings.Contains(w.Header().Get("Content-Security-Policy"), "unsafe-inline") {
				t.Fatal("unsafe CSP")
			}
		}
	}
}

func TestSignInReturnAcceptsOnlyLocalPaths(t *testing.T) {
	for _, target := range []string{"https://foreign.example/", "//foreign.example/", `/\foreign.example/`, "/%5cforeign.example/", "/%2f%2fforeign.example/", "/bad%0d%0aLocation:evil", "/%zz"} {
		if got := localTarget(target); got != "/" {
			t.Fatalf("unsafe return accepted: %q", got)
		}
	}
	for _, target := range []string{"/library?sort=name&filter=a%26b", "/files/%E6%AA%94%E6%A1%88?q=https://example.com/"} {
		if got := localTarget(target); got != target {
			t.Fatalf("local return changed: %q", got)
		}
	}
}

func TestFailureBrandUsesLocalPackageMetadata(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "Reader")
	images := filepath.Join(base, "target", "ui", "images")
	os.MkdirAll(images, 0700)
	os.WriteFile(filepath.Join(base, "INFO"), []byte("displayname=\"Reader & Notes\"\n"), 0600)
	// A small PNG header suffices to check data-URI encoding; real browser
	// coverage uses the package's complete icon.
	os.WriteFile(filepath.Join(images, "app_digest_72.png"), []byte("\x89PNG\r\n\x1a\n"), 0600)
	brand := readAppBrand(root, "Reader")
	if brand.name != "Reader & Notes" || !strings.HasPrefix(brand.icon, "data:image/png;base64,") {
		t.Fatal(brand)
	}
	os.WriteFile(filepath.Join(images, "app_digest_72.png"), []byte("<svg onload=bad()>"), 0600)
	if got := readAppBrand(root, "Reader"); got.icon != "" {
		t.Fatal("accepted non-PNG icon")
	}
	if got := readAppBrand(root, "../Reader"); got.icon != "" {
		t.Fatal("read outside package")
	}
}
