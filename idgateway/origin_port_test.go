package idgateway

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func originBaseConfig(t *testing.T) Config {
	return Config{
		Package: "App", Mode: ModeNone, Upstream: "unix:/var/packages/App/var/run/app.sock",
		IngressPath: "/synopkgland/App/", NativeIngress: true, StateDir: t.TempDir(), AuthorizeCGI: "/webman/3rdparty/App/sso/authorize.cgi",
		Origin: &OriginConfig{HTTPS: 5443, Upstream: "unix:/var/packages/App/var/run/dsm.sock", Cookies: OriginCookiesDSMPassthrough},
	}
}

func TestOriginConfigValidation(t *testing.T) {
	if err := originBaseConfig(t).validate(); err != nil {
		t.Fatalf("valid origin: %v", err)
	}
	for name, edit := range map[string]func(*Config){
		"port zero":         func(c *Config) { c.Origin.HTTPS = 0 },
		"port range":        func(c *Config) { c.Origin.HTTPS = 70000 },
		"reserved port":     func(c *Config) { c.Origin.HTTPS = 5001 },
		"tcp upstream":      func(c *Config) { c.Origin.Upstream = "http://127.0.0.1:8080" },
		"foreign socket":    func(c *Config) { c.Origin.Upstream = "unix:/var/packages/Other/var/run/dsm.sock" },
		"cookie mode":       func(c *Config) { c.Origin.Cookies = "everything" },
		"headers sans user": func(c *Config) { c.Origin.Headers = HeaderNames{UID: "X-Uid"} },
		"no native ingress": func(c *Config) { c.IngressPath, c.NativeIngress, c.Listen = "", false, Listen{HTTPS: 8443} },
		"listen port clash": func(c *Config) { c.IngressPath, c.Listen = "", Listen{HTTPS: 5443} },
		"issuer port clash": func(c *Config) {
			c.Mode, c.OIDC = ModeOIDC, &OIDCConfig{IssuerPort: 5443, Clients: []ClientConfig{{ID: "a"}}}
		},
		"compose upstream": func(c *Config) { c.ComposeIngress = &ComposeIngress{} },
	} {
		cfg := originBaseConfig(t)
		edit(&cfg)
		if err := cfg.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOriginPortComesFromTheInstallerSelection(t *testing.T) {
	payload := t.TempDir()
	cfg := originBaseConfig(t)
	cfg.StateDir = "/var/packages/App/var/sso"
	raw := `{"package":"App","mode":"none","upstream":"unix:/var/packages/App/var/run/app.sock","ingress_path":"/synopkgland/App/","native_ingress":true,"state_dir":"/var/packages/App/var/sso","origin":{"https":5443,"upstream":"unix:/var/packages/App/var/run/dsm.sock","cookies":"dsm-passthrough"}}`
	for path, content := range map[string]string{
		"sso/gateway.json":                         raw,
		"install/ports.json":                       `{"schema":1,"package":"App","ports":{"gateway_origin":{"default":5443,"protocol":"tcp","conflict":"warn"}},"files":{}}`,
		"install/port-values/gateway_origin.value": "6443",
	} {
		full := filepath.Join(payload, path)
		os.MkdirAll(filepath.Dir(full), 0o700)
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadConfig(filepath.Join(payload, "sso/gateway.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Origin == nil || got.Origin.HTTPS != 6443 {
		t.Fatalf("origin %+v", got.Origin)
	}
}

func TestDSMPassthroughCookies(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		for _, c := range []string{"_SSID=s2; Path=/", "stay_login=1; Path=/", "_CrPoSt=c2; Path=/", "id=remote; Path=/; HttpOnly", "did=remote; Path=/; HttpOnly", "sgr_a_id=remote; Path=/; HttpOnly", "synopkgland_sso_App_origin=forged; Path=/"} {
			w.Header().Add("Set-Cookie", c)
		}
		w.Write([]byte("dsm"))
	})
	f, c := newFront(t, ModeNone, up, func(cfg *FrontConfig) {
		cfg.Entry, cfg.Passthrough = "admin-only", nil
		cfg.Membership = &changingMembership{groups: []string{"administrators"}}
		cfg.CookieName = "synopkgland_sso_App_origin"
		cfg.Cookies = OriginCookiesDSMPassthrough
	})
	b := newBrowser(t, f)
	b.do("GET", dsmBounce(t, b.do("GET", "https://nas.example:5443/", navigate...), alice, c.t), navigate...)
	if _, ok := b.cookies["synopkgland_sso_App_origin"]; !ok {
		t.Fatalf("session cookie name: %v", b.cookies)
	}
	if _, ok := b.cookies["synopkgland_sso_App"]; ok {
		t.Fatalf("path gateway cookie name reused: %v", b.cookies)
	}
	w := b.do("GET", "https://nas.example:5443/webman/index.cgi", "Cookie", "id=host; did=host; _SSID=s1; _CrPoSt=c1; stay_login=1; sgr_a_id=r1; synopkgland_sso_App=other; spl_session_x=1")
	if w.Code != 200 || w.Body.String() != "dsm" {
		t.Fatalf("proxied: %d %s", w.Code, w.Body)
	}
	sent := map[string]bool{}
	for _, ck := range up.last.Cookies() {
		sent[ck.Name] = true
	}
	for _, name := range []string{"_SSID", "_CrPoSt", "stay_login", "sgr_a_id"} {
		if !sent[name] {
			t.Errorf("request cookie %s stripped: %v", name, up.last.Header.Get("Cookie"))
		}
	}
	for _, name := range []string{"id", "did", "synopkgland_sso_App", "synopkgland_sso_App_origin", "spl_session_x"} {
		if sent[name] {
			t.Errorf("request cookie %s forwarded: %v", name, up.last.Header.Get("Cookie"))
		}
	}
	var set []string
	for _, ck := range w.Result().Cookies() {
		set = append(set, ck.Name)
	}
	slices.Sort(set)
	if strings.Join(set, ",") != "_CrPoSt,_SSID,sgr_a_id,stay_login" {
		t.Fatalf("response cookies %v", set)
	}
}

func TestDefaultCookieModeStillStripsDSMCookies(t *testing.T) {
	up := newUpstream(t, nil)
	f, c := newFront(t, ModeNone, up, func(cfg *FrontConfig) {
		cfg.Entry, cfg.Passthrough = "admin-only", nil
		cfg.Membership = &changingMembership{groups: []string{"administrators"}}
	})
	b := newBrowser(t, f)
	b.do("GET", dsmBounce(t, b.do("GET", "https://nas.example:18443/", navigate...), alice, c.t), navigate...)
	b.do("GET", "https://nas.example:18443/", "Cookie", "_SSID=s1; stay_login=1; app=1")
	if ck := up.last.Header.Get("Cookie"); ck != "app=1" {
		t.Fatalf("cookies %q", ck)
	}
}

func TestOriginFrontIsAdminOnly(t *testing.T) {
	cfg := originBaseConfig(t)
	cfg.Origin.Headers = HeaderNames{User: "X-Relay-User", UID: "X-Relay-Uid"}
	gw, err := Build(cfg, DSMPorts{HTTP: 5000, HTTPS: 5001})
	if err != nil {
		t.Fatal(err)
	}
	if gw.Origin == nil {
		t.Fatal("no origin front")
	}
	o := gw.Origin.cfg
	if o.IngressPath != "" || o.Access != "dsm-admins" || o.Upstream != cfg.Origin.Upstream || o.Mode != ModeHeader || o.Headers.User != "X-Relay-User" || o.CookieName != "synopkgland_sso_App_origin" || o.Cookies != OriginCookiesDSMPassthrough {
		t.Fatalf("origin front config %+v", o)
	}
	if gw.Front.cfg.Upstream != cfg.Upstream || gw.Front.cookie != "synopkgland_sso_App" {
		t.Fatalf("path front changed: %+v", gw.Front.cfg)
	}
	cfg.Origin.Headers = HeaderNames{}
	gw, err = Build(cfg, DSMPorts{HTTP: 5000, HTTPS: 5001})
	if err != nil || gw.Origin.cfg.Mode != ModeNone {
		t.Fatalf("origin without headers: %v %v", err, gw.Origin.cfg.Mode)
	}
	cfg.Origin = nil
	if gw, err = Build(cfg, DSMPorts{}); err != nil || gw.Origin != nil {
		t.Fatalf("origin built without config: %v", err)
	}
}

func TestOriginFrontDeniesNonAdmins(t *testing.T) {
	up := newUpstream(t, nil)
	f, c := newFront(t, ModeNone, up, func(cfg *FrontConfig) {
		cfg.Entry, cfg.Passthrough = "admin-only", nil
		cfg.Membership = &changingMembership{groups: []string{"users"}}
		cfg.CookieName = "synopkgland_sso_App_origin"
		cfg.Cookies = OriginCookiesDSMPassthrough
	})
	b := newBrowser(t, f)
	b.do("GET", dsmBounce(t, b.do("GET", "https://nas.example:5443/", navigate...), bob, c.t), navigate...)
	before := up.hits
	if w := b.do("GET", "https://nas.example:5443/"); w.Code == 200 || up.hits != before {
		t.Fatalf("non-admin reached upstream: %d", w.Code)
	}
}

func TestOriginIngressHandlerChecksTheOriginPort(t *testing.T) {
	cfg := originBaseConfig(t)
	var hit bool
	h := originIngressHandler(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.RemoteAddr != "192.0.2.9:0" || r.Header.Get(nativeClientIPHeader) != "" || !requestSecure(r) {
			t.Errorf("request %s %v", r.RemoteAddr, r.Header)
		}
	}))
	send := func(host string, verified bool) int {
		r := httptest.NewRequest("GET", "https://"+host+"/webman/", nil)
		r.Host = host
		r.Header.Set(nativeClientIPHeader, "192.0.2.9")
		r.TLS = &tls.ConnectionState{}
		if verified {
			r.TLS.VerifiedChains = [][]*x509.Certificate{{nil}}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := send("nas.example:5443", false); code != http.StatusForbidden || hit {
		t.Fatalf("unauthenticated hop: %d", code)
	}
	if code := send("nas.example:5001", true); code != http.StatusBadRequest || hit {
		t.Fatalf("other port: %d", code)
	}
	if code := send("nas.example:5443", true); code != 200 || !hit {
		t.Fatalf("origin port: %d", code)
	}
}

func TestOriginSocketPathIsSeparate(t *testing.T) {
	if originSocketPath("App") != "/tmp/synopkgland-gateway-App-origin.sock" || originSocketPath("App") == nativeSocketPath("App") {
		t.Fatal(originSocketPath("App"))
	}
	if OriginVersion != 1 {
		t.Fatal("capability")
	}
}

func TestAuthorizeCGIAcceptsTheOriginReturn(t *testing.T) {
	dsm := &fakeDSM{user: "alice", token: "tok", addr: "192.0.2.7", admin: true}
	h := newCGI(dsm)
	h.IngressPath = "/synopkgland/App/"
	h.AppPorts = nil
	h.OriginPort = 5443
	h.LoginApp = "App"
	ok := "https://nas.example:5443" + ReturnPath
	w := cgiPost(t, h, cgiQuery(ok), "tok")
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || loc.Host != "nas.example:5443" || loc.Query().Get("assertion") == "" {
		t.Fatalf("origin return: %d %s", w.Code, loc)
	}
	a, err := OpenAssertion(testKey, loc.Query().Get("assertion"), cgiNow)
	if err != nil || a.Origin != "https://nas.example:5443" || !a.Subject.Admin {
		t.Fatalf("assertion %+v %v", a, err)
	}
	for _, bad := range []string{
		"http://nas.example:5443" + ReturnPath,
		"https://other.example:5443" + ReturnPath,
		"https://nas.example:5444" + ReturnPath,
		"https://nas.example:5443/synopkgland/App" + ReturnPath,
	} {
		if w := cgiPost(t, h, cgiQuery(bad), "tok"); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", bad, w.Code, w.Header().Get("Location"))
		}
	}
	// A cold DSM session never redirects into the main App's sign-in resume.
	w = cgiPost(t, h, cgiQuery(ok), "wrong")
	loc, _ = url.Parse(w.Header().Get("Location"))
	if loc.Host != "nas.example:5443" || loc.Query().Get("error") != "login_required" {
		t.Fatalf("cold origin sign-in: %d %s", w.Code, loc)
	}
}
