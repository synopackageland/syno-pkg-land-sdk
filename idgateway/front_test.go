package idgateway

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type upstream struct {
	*httptest.Server
	mu   sync.Mutex
	last *http.Request
	hits int
}

func newUpstream(t *testing.T, h http.HandlerFunc) *upstream {
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.last, u.hits = r.Clone(context.Background()), u.hits+1
		u.mu.Unlock()
		if h != nil {
			h(w, r)
			return
		}
		io.WriteString(w, "app")
	}))
	t.Cleanup(u.Close)
	return u
}

type fakeNames map[int]string

func (f fakeNames) Name(_ context.Context, uid int) (string, error) {
	if n, ok := f[uid]; ok {
		return n, nil
	}
	return "", ErrNoAccount
}

func newFront(t *testing.T, mode Mode, up *upstream, edit func(*FrontConfig)) (*Front, *clock) {
	t.Helper()
	c := &clock{time.Unix(1_700_000_000, 0)}
	cfg := FrontConfig{
		Mode:         mode,
		Upstream:     up.URL,
		Key:          testKey,
		AuthorizeCGI: "/webman/3rdparty/App/sso/authorize.cgi",
		DSMPorts:     DSMPorts{HTTP: 5000, HTTPS: 5001},
		Package:      "App",
		Headers:      HeaderNames{User: "Remote-User", Email: "Remote-Email", Groups: "Remote-Groups"},
		Roles:        Roles{Admin: "admin"},
		Passthrough:  []string{"/dav/", "/share/"},
		Accounts:     fakeNames{1026: "alice", 1027: "bob"},
		Now:          c.now,
	}
	if mode == ModeOIDC {
		p, _ := newProvider(t)
		p.now = c.now
		cfg.Provider = p
		cfg.Issuer = issuer
	}
	if edit != nil {
		edit(&cfg)
	}
	f, err := NewFront(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

type browser struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]string
	addr    string
}

func newBrowser(t *testing.T, h http.Handler) *browser {
	return &browser{t: t, h: h, cookies: map[string]string{}, addr: "192.0.2.7:4444"}
}

func (b *browser) do(method, target string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = b.addr
	if strings.HasPrefix(target, "https:") {
		r.TLS = &tls.ConnectionState{}
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Add(hdr[i], hdr[i+1])
	}
	for k, v := range b.cookies {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	return w
}

var navigate = []string{"Sec-Fetch-Mode", "navigate", "Accept", "text/html"}

// dsmBounce follows the gateway's redirect to the DSM CGI and returns the
// gateway return URL the CGI would issue for subject.
func dsmBounce(t *testing.T, w *httptest.ResponseRecorder, s Subject, now time.Time) string {
	t.Helper()
	loc, err := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || err != nil {
		t.Fatalf("expected DSM redirect, got %d %s", w.Code, w.Header().Get("Location"))
	}
	if loc.Path != "/webman/3rdparty/App/sso/authorize.cgi" {
		t.Fatalf("CGI path %s", loc)
	}
	ret, _ := url.Parse(loc.Query().Get("return"))
	sealed, _ := SealAssertion(testKey, Assertion{Txn: loc.Query().Get("txn"), Origin: ret.Scheme + "://" + ret.Host, Subject: s, Expires: now.Add(AssertionTTL).Unix()})
	return ret.String() + "?" + url.Values{"txn": {loc.Query().Get("txn")}, "assertion": {sealed}}.Encode()
}

func TestHeaderModeRedirectsANavigationWithoutSessionToTheDSMOrigin(t *testing.T) {
	f, _ := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	w := newBrowser(t, f).do("GET", "https://nas.example:18443/library?x=1", navigate...)
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || loc.Scheme != "https" || loc.Host != "nas.example:5001" || loc.Query().Get("return") != "https://nas.example:18443"+ReturnPath || loc.Query().Get("txn") == "" {
		t.Fatalf("%d %s", w.Code, loc)
	}
	w = newBrowser(t, f).do("GET", "http://nas.example:18080/", navigate...)
	if loc, _ := url.Parse(w.Header().Get("Location")); loc.Scheme != "http" || loc.Host != "nas.example:5000" {
		t.Fatalf("http bounce %s", loc)
	}
	if w := newBrowser(t, f).do("GET", "https://nas.example:18443/api/x", "Accept", "application/json"); w.Code != http.StatusUnauthorized {
		t.Fatalf("api without session: %d", w.Code)
	}
}

func TestHeaderModeSignsInAndInjectsTrustedHeaders(t *testing.T) {
	up := newUpstream(t, nil)
	f, c := newFront(t, ModeHeader, up, nil)
	b := newBrowser(t, f)
	ret := dsmBounce(t, b.do("GET", "https://nas.example:18443/library?x=1", navigate...), alice, c.t)
	w := b.do("GET", ret, navigate...)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/library?x=1" {
		t.Fatalf("return: %d %s %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	w = b.do("GET", "https://nas.example:18443/library", "Remote-User", "mallory", "Remote_User", "mallory", "X_Syno_User", "mallory", "X-Forwarded-For", "203.0.113.9", "X-Forwarded-Host", "evil.example", "X-Forwarded-Port", "65535", "Cookie", "app=1")
	if w.Code != 200 || w.Body.String() != "app" {
		t.Fatalf("proxied: %d %s", w.Code, w.Body)
	}
	got := up.last
	if got.Header.Values("Remote-User")[0] != "alice" || len(got.Header.Values("Remote-User")) != 1 || got.Header.Get("Remote-Groups") != "admin" || got.Header.Get("Remote-Email") != "alice@nas.invalid" {
		t.Fatalf("identity headers %v", got.Header)
	}
	if got.Header.Get("Remote_User") != "" || got.Header.Get("X_Syno_User") != "" {
		t.Fatalf("underscore identity headers %v", got.Header)
	}
	if got.Header.Get("X-Forwarded-For") != "192.0.2.7" || got.Header.Get("X-Forwarded-Host") != "nas.example:18443" || got.Header.Get("X-Forwarded-Proto") != "https" || got.Header.Get("X-Forwarded-Port") != "18443" || got.Host != "nas.example:18443" {
		t.Fatalf("forwarded %v host %s", got.Header, got.Host)
	}
	if ck := got.Header.Get("Cookie"); strings.Contains(ck, "synopkgland_sso") || !strings.Contains(ck, "app=1") {
		t.Fatalf("cookies to app: %q", ck)
	}
}

func TestHeaderModeUserMapping(t *testing.T) {
	up := newUpstream(t, nil)
	f, c := newFront(t, ModeHeader, up, func(cfg *FrontConfig) { cfg.Roles = Roles{Admin: "admin", User: "user"} })
	b := newBrowser(t, f)
	b.do("GET", dsmBounce(t, b.do("GET", "https://nas.example:18443/", navigate...), bob, c.t), navigate...)
	b.do("GET", "https://nas.example:18443/")
	if up.last.Header.Get("Remote-User") != "bob" || up.last.Header.Get("Remote-Groups") != "user" {
		t.Fatalf("bob %v", up.last.Header)
	}
}

func TestAssertionsAreBoundToTheirTransactionBrowserAndOrigin(t *testing.T) {
	f, c := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	b := newBrowser(t, f)
	ret := dsmBounce(t, b.do("GET", "https://nas.example:18443/", navigate...), alice, c.t)
	other := newBrowser(t, f)
	other.do("GET", "https://nas.example:18443/", navigate...)
	if w := other.do("GET", ret); w.Code != http.StatusBadRequest {
		t.Fatalf("other browser redeemed: %d", w.Code)
	}
	httpRet := strings.Replace(ret, "https://nas.example:18443", "http://nas.example:18080", 1)
	if w := b.do("GET", httpRet); w.Code != http.StatusBadRequest {
		t.Fatalf("other origin redeemed: %d", w.Code)
	}
	if w := b.do("GET", ret); w.Code != http.StatusFound {
		t.Fatalf("own browser: %d %s", w.Code, w.Body)
	}
	if w := b.do("GET", ret); w.Code != http.StatusBadRequest {
		t.Fatalf("replayed: %d", w.Code)
	}
	late := newBrowser(t, f)
	ret = dsmBounce(t, late.do("GET", "https://nas.example:18443/", navigate...), alice, c.t)
	c.t = c.t.Add(AssertionTTL + time.Second)
	if w := late.do("GET", ret); w.Code != http.StatusBadRequest {
		t.Fatalf("expired: %d", w.Code)
	}
}

func TestSessionsEndWhenTheAccountIsDeletedOrRenamedOrMoves(t *testing.T) {
	up := newUpstream(t, nil)
	names := fakeNames{1026: "alice", 1027: "bob"}
	f, c := newFront(t, ModeHeader, up, func(cfg *FrontConfig) { cfg.Accounts = names })
	signIn := func() *browser {
		b := newBrowser(t, f)
		b.do("GET", dsmBounce(t, b.do("GET", "https://nas.example:18443/", navigate...), bob, c.t))
		return b
	}
	b := signIn()
	b.addr = "198.51.100.3:1"
	if w := b.do("GET", "https://nas.example:18443/x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("other address: %d", w.Code)
	}
	b = signIn()
	delete(names, 1027)
	if w := b.do("GET", "https://nas.example:18443/x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("deleted: %d", w.Code)
	}
	names[1027] = "bob"
	if w := b.do("GET", "https://nas.example:18443/x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("session revived: %d", w.Code)
	}
	b = signIn()
	names[1027] = "robert"
	if w := b.do("GET", "https://nas.example:18443/x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("renamed: %d", w.Code)
	}
	names[1027] = "bob"
	b = signIn()
	c.t = c.t.Add(DefaultSessionTTL + time.Second)
	if w := b.do("GET", "https://nas.example:18443/x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("expired: %d", w.Code)
	}
}

func TestPassthroughPathsReachTheAppWithoutIdentity(t *testing.T) {
	up := newUpstream(t, nil)
	f, _ := newFront(t, ModeHeader, up, nil)
	w := newBrowser(t, f).do("PROPFIND", "http://nas.example:18080/dav/music", "Remote-User", "admin", "X-Forwarded-For", "192.0.2.1", "Authorization", "Basic eDp5")
	if w.Code != 200 || up.last.Header.Get("Remote-User") != "" || up.last.Header.Get("X-Forwarded-For") != "192.0.2.7" || up.last.Header.Get("Authorization") != "Basic eDp5" || up.last.URL.Path != "/dav/music" {
		t.Fatalf("%d %v", w.Code, up.last.Header)
	}
	if w := newBrowser(t, f).do("GET", "http://nas.example:18080/davx", "Accept", "application/json"); w.Code != http.StatusUnauthorized {
		t.Fatalf("prefix must end at a segment: %d", w.Code)
	}
}

func TestOIDCModeRewritesTheProviderRedirectToTheAppOrigin(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.Redirect(w, r, issuer+"/authorize?client_id=paperless&state=s", http.StatusFound)
		case "/self":
			http.Redirect(w, r, "http://"+r.Context().Value(http.LocalAddrContextKey).(interface{ String() string }).String()+"/home", http.StatusFound)
		case "/api/oauth/authorize":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"url":"`+issuer+`/authorize?client_id=immich&x=1","other":"`+issuer+`/authorize"}`)
		default:
			io.WriteString(w, "app")
		}
	})
	f, _ := newFront(t, ModeOIDC, up, func(cfg *FrontConfig) {
		cfg.RewriteJSON = []JSONRewrite{{Method: "POST", Path: "/api/oauth/authorize", Field: "url"}}
	})
	b := newBrowser(t, f)
	if loc := b.do("GET", "https://nas.example:18443/login").Header().Get("Location"); loc != "https://nas.example:18443/_sso/authorize?client_id=paperless&state=s" {
		t.Fatalf("location %q", loc)
	}
	if loc := b.do("GET", "https://nas.example:18443/self").Header().Get("Location"); loc != "https://nas.example:18443/home" {
		t.Fatalf("upstream origin location %q", loc)
	}
	w := b.do("POST", "http://nas.example:18080/api/oauth/authorize", "Origin", "http://nas.example:18080")
	var body map[string]string
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || body["url"] != "http://nas.example:18080/_sso/authorize?client_id=immich&x=1" || body["other"] != issuer+"/authorize" {
		t.Fatalf("json %s", w.Body)
	}
	if w.Header().Get("Content-Length") != "" && w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
		t.Fatalf("content-length %s vs %d", w.Header().Get("Content-Length"), w.Body.Len())
	}
	if w := b.do("GET", "https://nas.example:18443/anything", "Remote-User", "mallory"); w.Code != 200 || up.last.Header.Get("Remote-User") != "" {
		t.Fatalf("oidc mode passes the app through, without identity headers: %d %v", w.Code, up.last.Header)
	}
}

func TestOIDCModeIssuesACodeAfterTheDSMHop(t *testing.T) {
	f, c := newFront(t, ModeOIDC, newUpstream(t, nil), nil)
	b := newBrowser(t, f)
	redirect := "https://nas.example:18443/accounts/oidc/dsm/login/callback/"
	authorize := "https://nas.example:18443/_sso/authorize?" + authQuery("paperless", redirect, "v").Encode()
	w := b.do("GET", dsmBounce(t, b.do("GET", authorize), alice, c.t))
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || !strings.HasPrefix(loc.String(), redirect+"?") || loc.Query().Get("code") == "" || loc.Query().Get("state") != "st" {
		t.Fatalf("%d %s", w.Code, loc)
	}
	if w := b.do("GET", "https://nas.example:18443/_sso/authorize?"+authQuery("paperless", "https://evil.example/cb", "v").Encode()); w.Code != http.StatusBadRequest {
		t.Fatalf("foreign redirect: %d", w.Code)
	}
	if loc := b.do("GET", "https://nas.example:18443/_sso/authorize?"+authQuery("paperless", redirect, "").Encode()).Header().Get("Location"); !strings.Contains(loc, "error=invalid_request") {
		t.Fatalf("missing pkce: %s", loc)
	}
}

func TestOIDCLoginRequiredFollowsPrompt(t *testing.T) {
	f, _ := newFront(t, ModeOIDC, newUpstream(t, nil), nil)
	redirect := "https://nas.example:18443/accounts/oidc/dsm/login/callback/"
	for prompt, want := range map[string]int{"none": http.StatusFound, "": http.StatusUnauthorized} {
		b := newBrowser(t, f)
		loc, _ := url.Parse(b.do("GET", "https://nas.example:18443/_sso/authorize?"+authQuery("paperless", redirect, "v", "prompt", prompt).Encode()).Header().Get("Location"))
		w := b.do("GET", loc.Query().Get("return")+"?"+url.Values{"txn": {loc.Query().Get("txn")}, "error": {"login_required"}}.Encode())
		if w.Code != want {
			t.Fatalf("prompt %q: %d", prompt, w.Code)
		}
		if prompt == "none" && !strings.Contains(w.Header().Get("Location"), "error=login_required") {
			t.Fatalf("prompt none: %s", w.Header().Get("Location"))
		}
		if prompt == "" && !strings.Contains(w.Body.String(), `href="https://nas.example:5001/"`) {
			t.Fatalf("sign-in page: %s", w.Body)
		}
	}
}

func TestLoginRedirectsAndUnknownSSOPaths(t *testing.T) {
	f, _ := newFront(t, ModeOIDC, newUpstream(t, nil), func(cfg *FrontConfig) {
		cfg.Redirects = map[string]string{"/@login": "/api/auth/sso?method=sso_get_token"}
	})
	b := newBrowser(t, f)
	if w := b.do("GET", "https://nas.example:18443/@login"); w.Code != http.StatusFound || w.Header().Get("Location") != "/api/auth/sso?method=sso_get_token" {
		t.Fatalf("%d %s", w.Code, w.Header().Get("Location"))
	}
	if w := b.do("GET", "https://nas.example:18443/_sso/token"); w.Code != http.StatusNotFound {
		t.Fatalf("/_sso/token: %d", w.Code)
	}
}

type staticTokens struct{ calls int }

func (s *staticTokens) Credentials(_ context.Context, sub Subject) (http.Header, error) {
	s.calls++
	return http.Header{"Authorization": {"Bearer app-token-" + sub.Name}}, nil
}

func TestTokenModeInjectsAdapterCredentials(t *testing.T) {
	up := newUpstream(t, nil)
	src := &staticTokens{}
	f, c := newFront(t, ModeToken, up, func(cfg *FrontConfig) { cfg.Tokens = src })
	b := newBrowser(t, f)
	b.do("GET", dsmBounce(t, b.do("GET", "https://nas.example:18443/", navigate...), bob, c.t))
	b.do("GET", "https://nas.example:18443/api/me", "Authorization", "Bearer forged")
	if up.last.Header.Get("Authorization") != "Bearer app-token-bob" || up.last.Header.Get("Remote-User") != "" {
		t.Fatalf("%v", up.last.Header)
	}
	w := newBrowser(t, f).do("GET", "https://nas.example:18443/dav/x", "Authorization", "Basic eDp5")
	if w.Code != 200 || up.last.Header.Get("Authorization") != "Basic eDp5" {
		t.Fatalf("passthrough keeps the client's own credentials: %v", up.last.Header)
	}
}

func TestLogoutEndsTheGatewaySession(t *testing.T) {
	f, c := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	b := newBrowser(t, f)
	b.do("GET", dsmBounce(t, b.do("GET", "https://nas.example:18443/", navigate...), bob, c.t))
	if w := b.do("POST", "https://nas.example:18443/_sso/logout", "Origin", "https://nas.example:18443"); w.Code != http.StatusFound {
		t.Fatalf("logout %d", w.Code)
	}
	if w := b.do("GET", "https://nas.example:18443/x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", w.Code)
	}
}

func TestNewFrontValidatesItsConfiguration(t *testing.T) {
	up := newUpstream(t, nil)
	for name, edit := range map[string]func(*FrontConfig){
		"public upstream":   func(c *FrontConfig) { c.Upstream = "http://192.0.2.1:8000" },
		"oidc no provider":  func(c *FrontConfig) { c.Mode = ModeOIDC; c.Provider = nil },
		"header no user":    func(c *FrontConfig) { c.Headers.User = "" },
		"token no adapter":  func(c *FrontConfig) { c.Mode = ModeToken; c.Tokens = nil },
		"short key":         func(c *FrontConfig) { c.Key = []byte("k") },
		"relative passthru": func(c *FrontConfig) { c.Passthrough = []string{"dav/"} },
		"sso passthrough":   func(c *FrontConfig) { c.Passthrough = []string{"/_sso/"} },
		"unknown mode":      func(c *FrontConfig) { c.Mode = "magic" },
	} {
		cfg := FrontConfig{Mode: ModeHeader, Upstream: up.URL, Key: testKey, AuthorizeCGI: "/webman/3rdparty/App/sso/authorize.cgi", DSMPorts: DSMPorts{5000, 5001}, Package: "App", Headers: HeaderNames{User: "Remote-User"}, Accounts: fakeNames{}}
		edit(&cfg)
		if _, err := NewFront(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestDotSegmentsNeverReachTheApp(t *testing.T) {
	up := newUpstream(t, nil)
	f, _ := newFront(t, ModeHeader, up, nil)
	for _, target := range []string{"http://nas.example:18080/dav/../admin", "http://nas.example:18080/dav/%2e%2e/admin", "http://nas.example:18080/dav/..%2Fadmin", `http://nas.example:18080/dav/..\admin`} {
		if w := newBrowser(t, f).do("GET", target); w.Code != http.StatusBadRequest || up.hits != 0 {
			t.Errorf("%s: %d hits=%d", target, w.Code, up.hits)
		}
	}
}

func TestRevokeUIDEndsOnlyThatAccountsSessions(t *testing.T) {
	up := newUpstream(t, nil)
	f, c := newFront(t, ModeHeader, up, nil)
	signIn := func(s Subject) *browser {
		b := newBrowser(t, f)
		b.do("GET", dsmBounce(t, b.do("GET", "https://nas.example:18443/", navigate...), s, c.t))
		return b
	}
	a, b := signIn(alice), signIn(bob)
	f.RevokeUID(bob.UID)
	if w := b.do("GET", "https://nas.example:18443/x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked: %d", w.Code)
	}
	if w := a.do("GET", "https://nas.example:18443/x"); w.Code != http.StatusOK {
		t.Fatalf("other account: %d", w.Code)
	}
}

func TestSignedInSeesEachVerifiedSignIn(t *testing.T) {
	for _, mode := range []Mode{ModeHeader, ModeOIDC} {
		var got []Subject
		f, c := newFront(t, mode, newUpstream(t, nil), func(cfg *FrontConfig) { cfg.SignedIn = func(s Subject) { got = append(got, s) } })
		b := newBrowser(t, f)
		start := "https://nas.example:18443/"
		if mode == ModeOIDC {
			start += "_sso/authorize?" + authQuery("paperless", "https://nas.example:18443/accounts/oidc/dsm/login/callback/", "v").Encode()
		}
		ret := dsmBounce(t, b.do("GET", start, navigate...), bob, c.t)
		b.do("GET", ret)
		b.do("GET", ret) // a replayed return is not a sign-in
		if len(got) != 1 || got[0] != bob {
			t.Fatalf("%s: %v", mode, got)
		}
	}
}
