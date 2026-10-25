package idgateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type fakeDSM struct {
	user, token, addr string
	admin             bool
	seen              *http.Request
}

func (f *fakeDSM) Authenticate(r *http.Request) (api.Identity, error) {
	f.seen = r
	if r.Header.Get("Cookie") != "id=s1" || r.Header.Get("X-SYNO-TOKEN") != f.token || !strings.HasPrefix(r.RemoteAddr, f.addr+":") {
		return api.Identity{}, api.ErrNoSession
	}
	return api.Identity{User: f.user, Admin: f.admin}, nil
}

type fakeAccounts map[string]int

func (f fakeAccounts) UID(_ context.Context, name string) (int, error) {
	if uid, ok := f[name]; ok {
		return uid, nil
	}
	return 0, ErrNoAccount
}

var cgiNow = time.Unix(1_700_000_000, 0)

func newCGI(dsm *fakeDSM) *AuthorizeCGI {
	return &AuthorizeCGI{Key: testKey, AppPorts: []int{18080, 18443}, Verifier: dsm, Accounts: fakeAccounts{"alice": 1026}, Now: func() time.Time { return cgiNow }}
}

const cgiURL = "https://nas.example:5001/webman/3rdparty/App/sso/authorize.cgi"

func cgiQuery(ret string) string {
	return "?" + url.Values{"txn": {"t1"}, "return": {ret}}.Encode()
}

func cgiPost(t *testing.T, h http.Handler, query, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, cgiURL+query, strings.NewReader(url.Values{"SynoToken": {token}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Cookie", "id=s1")
	r.Header.Set("Origin", "https://nas.example:5001")
	r.RemoteAddr = "192.0.2.7:5555"
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCGIBootstrapPageFetchesTheSynoTokenSameOrigin(t *testing.T) {
	w := httptest.NewRecorder()
	newCGI(&fakeDSM{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, cgiURL+cgiQuery("https://nas.example:18443/_sso/dsm-return"), nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "/webman/login.cgi?enable_syno_token=yes") || !strings.Contains(body, `method="post"`) {
		t.Fatalf("%d %s", w.Code, body)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "connect-src 'self'") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", w.Header())
	}
}

func TestCGIBootstrapFragmentNavigation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("bootstrap script tests require Node, provided by the SDK gate")
	}
	h := newCGI(&fakeDSM{})
	h.External = func() External {
		return External{Accept: []string{"https://app.example.com"}, R1: 1, R2: 2}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", cgiURL+"?resume=opaque:x0:1.2", nil))
	page := filepath.Join(t.TempDir(), "bootstrap.html")
	if err := os.WriteFile(page, w.Body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--test", "authorize_bootstrap.test.mjs")
	cmd.Env = append(os.Environ(), "SPL_AUTHORIZE_BOOTSTRAP="+page)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap navigation: %v\n%s", err, out)
	}
}

// Browsers send "Origin: null" on a form POST under Referrer-Policy
// no-referrer, which the CGI's own Origin check then refuses (builder.example, Chromium).
func TestCGIBootstrapKeepsTheOriginOfItsOwnPost(t *testing.T) {
	w := httptest.NewRecorder()
	newCGI(&fakeDSM{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, cgiURL+cgiQuery("https://nas.example:18443/_sso/dsm-return"), nil))
	if policy := w.Header().Get("Referrer-Policy"); policy != "same-origin" {
		t.Fatalf("Referrer-Policy %q", policy)
	}
}

// Chromium applies form-action to the redirect after the form POST, so the
// policy must allow the gateway's validated return origin and nothing else.
func TestCGIBootstrapLetsItsPostRedirectOnlyToTheGateway(t *testing.T) {
	w := httptest.NewRecorder()
	newCGI(&fakeDSM{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, cgiURL+cgiQuery("https://nas.example:18443/_sso/dsm-return"), nil))
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' https://nas.example:18443;") {
		t.Fatalf("CSP %q", csp)
	}
}

func TestCGIIssuesAnAssertionForAVerifiedSessionAndAccount(t *testing.T) {
	dsm := &fakeDSM{user: "alice", admin: true, token: "tok", addr: "192.0.2.7"}
	w := cgiPost(t, newCGI(dsm), cgiQuery("https://nas.example:18443/_sso/dsm-return"), "tok")
	if w.Code != http.StatusFound {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Scheme+"://"+loc.Host+loc.Path != "https://nas.example:18443/_sso/dsm-return" || loc.Query().Get("txn") != "t1" {
		t.Fatalf("location %s", loc)
	}
	a, err := OpenAssertion(testKey, loc.Query().Get("assertion"), cgiNow)
	want := Assertion{Txn: "t1", Origin: "https://nas.example:18443", Subject: Subject{UID: 1026, Name: "alice", Admin: true}, Expires: cgiNow.Add(AssertionTTL).Unix()}
	if err != nil || a != want {
		t.Fatalf("assertion %+v %v", a, err)
	}
	if dsm.seen.Header.Get("X-SYNO-TOKEN") != "tok" {
		t.Fatal("token not handed to DSM")
	}
}

func TestCGIReturnsLoginRequiredWithoutAValidSession(t *testing.T) {
	ret := cgiQuery("http://nas.example:18080/_sso/dsm-return")
	for name, tc := range map[string]struct {
		dsm   *fakeDSM
		token string
	}{
		"wrong token":     {&fakeDSM{user: "alice", token: "tok", addr: "192.0.2.7"}, "bad"},
		"wrong address":   {&fakeDSM{user: "alice", token: "tok", addr: "198.51.100.1"}, "tok"},
		"deleted account": {&fakeDSM{user: "bob", token: "tok", addr: "192.0.2.7"}, "tok"},
	} {
		w := cgiPost(t, newCGI(tc.dsm), ret, tc.token)
		loc, _ := url.Parse(w.Header().Get("Location"))
		if w.Code != http.StatusFound || loc.Query().Get("assertion") != "" || loc.Query().Get("error") != "login_required" || loc.Host != "nas.example:18080" {
			t.Errorf("%s: %d %s", name, w.Code, loc)
		}
	}
}

func TestCGIRefusesReturnURLsOutsideTheGateway(t *testing.T) {
	h := newCGI(&fakeDSM{user: "alice", token: "tok", addr: "192.0.2.7"})
	for _, ret := range []string{
		"https://evil.example:18443/_sso/dsm-return",
		"https://nas.example:18444/_sso/dsm-return",
		"https://nas.example/_sso/dsm-return",
		"https://nas.example:18443/callback",
		"https://nas.example:18443/_sso/dsm-return?x=1",
		"https://u@nas.example:18443/_sso/dsm-return",
		"javascript://nas.example:18443/_sso/dsm-return",
		"/_sso/dsm-return",
	} {
		w := cgiPost(t, h, cgiQuery(ret), "tok")
		if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
			t.Errorf("%s: %d %s", ret, w.Code, w.Header().Get("Location"))
		}
	}
	w := cgiPost(t, h, "?"+url.Values{"txn": {"<bad>"}, "return": {"https://nas.example:18443/_sso/dsm-return"}}.Encode(), "tok")
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad txn: %d", w.Code)
	}
}

func TestCGIRefusesCrossOriginPosts(t *testing.T) {
	h := newCGI(&fakeDSM{user: "alice", token: "tok", addr: "192.0.2.7"})
	r := httptest.NewRequest(http.MethodPost, cgiURL+cgiQuery("https://nas.example:18443/_sso/dsm-return"), strings.NewReader("SynoToken=tok"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Cookie", "id=s1")
	r.Header.Set("Origin", "https://nas.example:18443")
	r.RemoteAddr = "192.0.2.7:5555"
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("%d", w.Code)
	}
}

func TestCGIFailsClosedWhenTheVerifierErrs(t *testing.T) {
	h := newCGI(&fakeDSM{})
	h.Verifier = verifierFunc(func(*http.Request) (api.Identity, error) { return api.Identity{}, errors.New("DSM down") })
	w := cgiPost(t, h, cgiQuery("https://nas.example:18443/_sso/dsm-return"), "tok")
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Query().Get("error") != "temporarily_unavailable" || loc.Query().Get("assertion") != "" {
		t.Fatalf("%s", loc)
	}
}

func TestCGIAppPrivilegeDenialPreservesAccessDeniedReason(t *testing.T) {
	h := newCGI(&fakeDSM{})
	h.LoginApp = "Fixture"
	h.Verifier = verifierFunc(func(*http.Request) (api.Identity, error) {
		return api.Identity{}, fmt.Errorf("wrapped: %w", api.ErrAppPrivilegeDenied)
	})
	w := cgiPost(t, h, cgiQuery("https://nas.example:18443/_sso/dsm-return"), "tok")
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil || w.Code != http.StatusFound || loc.Path != ReturnPath || loc.Query().Get("error") != "access_denied" || loc.Query().Get("error_reason") != "app_privilege_denied" || loc.Query().Get("assertion") != "" {
		t.Fatalf("App privilege denial: %d %s %v", w.Code, loc, err)
	}
}

type verifierFunc func(*http.Request) (api.Identity, error)

func (f verifierFunc) Authenticate(r *http.Request) (api.Identity, error) { return f(r) }
