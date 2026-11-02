package idgateway

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const appPath = "/synopkgland/App/"

func TestPathIngressHeaderSignInAndScope(t *testing.T) {
	up := newUpstream(t, nil)
	f, clock := newFront(t, ModeHeader, up, func(c *FrontConfig) { c.IngressPath = appPath })
	r := httptest.NewRequest("GET", "https://nas.example:5001"+appPath+"app/", nil)
	r.Header.Set("Accept", "text/html")
	out := httptest.NewRecorder()
	f.ServeHTTP(out, r)
	if out.Code != 302 {
		t.Fatal(out.Code, out.Body.String())
	}
	dest, _ := url.Parse(out.Header().Get("Location"))
	ret := dest.Query().Get("return")
	if ret != "https://nas.example:5001"+appPath+"_sso/dsm-return" {
		t.Fatal(dest)
	}
	cookies := out.Result().Cookies()
	for _, c := range cookies {
		if c.Path != appPath {
			t.Fatal("cookie escaped package", c)
		}
	}
	sealed, err := SealAssertion(testKey, Assertion{Txn: dest.Query().Get("txn"), Origin: "https://nas.example:5001", Subject: Subject{UID: 1026, Name: "alice"}, Expires: clock.now().Add(AssertionTTL).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", ret+"?txn="+dest.Query().Get("txn")+"&assertion="+sealed, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	out = httptest.NewRecorder()
	f.ServeHTTP(out, r)
	if out.Code != 302 || out.Header().Get("Location") != appPath+"app/" {
		t.Fatal(out.Code, out.Header(), out.Body.String())
	}
	for _, c := range out.Result().Cookies() {
		if c.Path != appPath {
			t.Fatal(c)
		}
		cookies = append(cookies, c)
	}
	r = httptest.NewRequest("GET", "https://nas.example:5001"+appPath+"api/data?q=1", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	r.Header.Set("Cookie", r.Header.Get("Cookie")+"; id=DSM_SECRET; synopkgland_sso_Other=OTHER")
	out = httptest.NewRecorder()
	f.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.last.URL.Path != appPath+"api/data" || up.last.Header.Get("Remote-User") != "alice" || strings.Contains(up.last.Header.Get("Cookie"), "SECRET") || strings.Contains(up.last.Header.Get("Cookie"), "OTHER") {
		t.Fatal(up.last)
	}
}

func TestPathIngressNativeBoundaryAndNoSiblingPaths(t *testing.T) {
	cfg := Config{Package: "App", NativeIngress: true, IngressPath: appPath}
	h := nativeIngressHandler(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		path   string
		status int
	}{{appPath, 204}, {"/synopkgland/Other/", 404}, {"/_sso/authorize", 404}, {appPath + "../Other/", 400}} {
		r := httptest.NewRequest("GET", "https://nas.example:5001"+tc.path, nil)
		r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
		r.Header.Set(nativeClientIPHeader, "192.0.2.7")
		r.Header.Set("X-Synopkgland-Scheme", "https")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestPathIngressAuthorizeReturnIsSameOriginAndPackageOnly(t *testing.T) {
	h := AuthorizeCGI{IngressPath: appPath}
	for _, tc := range []struct {
		ret string
		ok  bool
	}{{"https://nas.example:5001" + appPath + "_sso/dsm-return", true}, {"https://evil.example:5001" + appPath + "_sso/dsm-return", false}, {"https://nas.example:5001/synopkgland/Other/_sso/dsm-return", false}, {"https://nas.example:5001/_sso/dsm-return", false}, {"https://nas.example:5001" + appPath + "_sso/dsm-return?x=1", false}} {
		r := httptest.NewRequest("GET", "https://nas.example:5001/authorize.cgi?return="+url.QueryEscape(tc.ret), nil)
		_, ok := h.returnURL(r)
		if ok != tc.ok {
			t.Errorf("%s: %v", tc.ret, ok)
		}
	}
}

func TestPathIngressNoneAndOIDCConfiguration(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != appPath+"app/" {
			t.Errorf("path not preserved: %s", r.URL.Path)
		}
		w.Header().Set("Set-Cookie", "native=session; Path=/; Domain=nas.example; HttpOnly")
		w.WriteHeader(200)
	})
	f, _ := newFront(t, ModeNone, up, func(c *FrontConfig) { c.IngressPath = appPath; c.Access = "public" })
	r := httptest.NewRequest("GET", "https://nas.example:5001"+appPath+"app/", nil)
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Path != appPath || cookie.Domain != "" {
			t.Fatal(cookie)
		}
	}
	cfg := Config{Package: "App", NativeIngress: true, IngressPath: appPath, Mode: ModeOIDC, Access: "dsm-users", Upstream: up.URL, StateDir: t.TempDir(), AuthorizeCGI: "/webman/3rdparty/App/sso/authorize.cgi", OIDC: &OIDCConfig{IssuerPort: 13380, Clients: []ClientConfig{{ID: "app", RedirectPaths: []string{"/callback"}}}}}
	gw, err := Build(cfg, DSMPorts{HTTP: 5000, HTTPS: 5001})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		ok   bool
	}{{appPath + "callback", true}, {"/callback", false}, {"/synopkgland/Other/callback", false}} {
		q := url.Values{"client_id": {"app"}, "response_type": {"code"}, "redirect_uri": {"https://nas.example:5001" + tc.path}, "scope": {"openid"}}
		_, err := gw.Provider.Begin("https://nas.example:5001", q)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.path, err)
		}
	}
	if got := gw.Front.publicURL(cfg.Issuer()+"/authorize?client_id=app", "https://nas.example:5001"); got != "https://nas.example:5001"+appPath+"_sso/authorize?client_id=app" {
		t.Fatal(got)
	}
}

func TestPathIngressColdMenuResumeKeepsDSMOrigin(t *testing.T) {
	cfg := Config{Package: "App", NativeIngress: true, IngressPath: appPath, AuthorizeCGI: "/webman/3rdparty/App/sso/authorize.cgi", LaunchPath: "/app/#/song"}
	menu := entryHandler(cfg, func() error { return nil })
	for _, query := range []string{"", "?ssoResume=transaction:p"} {
		r := httptest.NewRequest("GET", "https://nas.example:5001/webman/3rdparty/App/sso/open.cgi"+query, nil)
		w := httptest.NewRecorder()
		menu.ServeHTTP(w, r)
		if w.Code != 302 {
			t.Fatal(w.Code, w.Body.String())
		}
		dest, _ := url.Parse(w.Header().Get("Location"))
		if query == "" {
			if dest.String() != "https://nas.example:5001"+appPath+"app/#/song" {
				t.Fatal(dest)
			}
		} else {
			if dest.Host != "nas.example:5001" || dest.Path != cfg.AuthorizeCGI || dest.Query().Get("return") != "https://nas.example:5001"+appPath+"_sso/dsm-return" {
				t.Fatal(dest)
			}
		}
	}
	h := AuthorizeCGI{IngressPath: appPath}
	r := httptest.NewRequest("GET", "https://nas.example:5001/authorize.cgi?resume=transaction:p", nil)
	w := httptest.NewRecorder()
	req, ok := h.prepareResume(w, r, External{})
	if !ok || req.URL.Query().Get("return") != "https://nas.example:5001"+appPath+"_sso/dsm-return" {
		t.Fatal(ok, w.Code)
	}
}
