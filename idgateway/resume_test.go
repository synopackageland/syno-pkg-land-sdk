package idgateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestResumeParserSharedGrammar(t *testing.T) {
	for _, raw := range []string{"txn:18443", "A_-:x0:0.0", "txn:x1:9999999999999999999.9007199254740993", "txn:x0:0000000000000000001.02"} {
		token, ok := parseResume(raw)
		if !ok || token.Txn == "" {
			t.Fatal(raw, token, ok)
		}
	}
	for _, raw := range []string{"", ":x0:0.0", "../txn:x0:0.0", "txn:0", "txn:65536", "txn:018443", "txn:x2:0.0", "txn:x00:0.0", "txn:x0:-1.0", "txn:x0:+1.0", "txn:x0:1e3.0", "txn:x0:18446744073709551615.0", "txn:x0:1.0:tail", "txn:x0:1.", "txn:x0:.0", "txn:x0:１.0", strings.Repeat("a", 129) + ":x0:0.0"} {
		if token, ok := parseResume(raw); ok {
			t.Fatal("accepted", raw, token)
		}
	}
}

func TestExternalWarmSignInModes(t *testing.T) {
	for _, mode := range []Mode{ModeHeader, ModeNone, ModeOIDC} {
		e := External{Accept: []string{"https://app.example.com"}, DSM: "https://dsm.example.com", R1: 9, R2: 17}
		f, _ := newFront(t, mode, newUpstream(t, nil), func(c *FrontConfig) { c.External = func() External { return e } })
		b := newBrowser(t, f)
		target := e.Accept[0] + "/"
		if mode == ModeOIDC {
			target += "_sso/authorize?" + authQuery("paperless", e.Accept[0]+"/accounts/oidc/dsm/login/callback/", "v").Encode()
		}
		start := b.do("GET", target, navigate...)
		u, _ := url.Parse(start.Header().Get("Location"))
		if start.Code != 302 || u.Host != "dsm.example.com" || u.Query().Get("return") != e.Accept[0]+ReturnPath {
			t.Fatal(mode, start.Code, start.Header())
		}
		cgi := newCGI(&fakeDSM{user: "alice", token: "tok", addr: "192.0.2.7"})
		cgi.External = func() External { return e }
		done := cgiPost(t, cgi, "?"+u.RawQuery, "tok")
		if done.Code != 302 {
			t.Fatal(mode, done.Code, done.Body)
		}
		ret, _ := url.Parse(done.Header().Get("Location"))
		a, err := OpenAssertion(testKey, ret.Query().Get("assertion"), cgiNow)
		if err != nil || a.Origin != e.Accept[0] {
			t.Fatal(mode, a, err, ret)
		}
		final := b.do("GET", ret.String(), navigate...)
		if final.Code != 302 || (final.Header().Get("Location") != "/" && !strings.HasPrefix(final.Header().Get("Location"), e.Accept[0]+"/")) {
			t.Fatal(mode, final.Code, final.Body, final.Header())
		}
		for _, cookie := range final.Result().Cookies() {
			if !cookie.Secure || cookie.Domain != "" {
				t.Fatal(cookie)
			}
		}
		if len(f.sessions) == 0 && mode != ModeOIDC {
			t.Fatal("session not established", mode)
		}
	}
}

func TestExternalColdLoginLauncherEntryAndCGIPreserveToken(t *testing.T) {
	for _, index := range []int{0, 1} {
		e := External{Accept: []string{"https://manual.example.com", "https://connect.example.com"}, DSM: "https://dsm.example.com", R1: 9007199254740993, R2: 9999999999999999999}
		f, _ := newFront(t, ModeOIDC, newUpstream(t, nil), func(c *FrontConfig) { c.External = func() External { return e } })
		b := newBrowser(t, f)
		origin := e.Accept[index]
		start := b.do("GET", origin+"/_sso/authorize?"+authQuery("paperless", origin+"/accounts/oidc/dsm/login/callback/", "v").Encode(), navigate...)
		u, _ := url.Parse(start.Header().Get("Location"))
		resume := u.Query().Get("resume")
		if !strings.HasSuffix(resume, ":9007199254740993.9999999999999999999") {
			t.Fatal("lost revision precision", resume)
		}
		cgi := newCGI(&fakeDSM{})
		cgi.External = func() External { return e }
		cgi.LoginApp = "App"
		launch := cgiPost(t, cgi, "?"+u.RawQuery, "")
		launcher, _ := url.Parse(launch.Header().Get("Location"))
		var param map[string]string
		if json.Unmarshal([]byte(launcher.Query().Get("launchParam")), &param) != nil || param["ssoResume"] != resume {
			t.Fatal("launcher lost token", launcher)
		}
		cfg := Config{Package: "App", Mode: ModeOIDC, NativeIngress: true, Listen: Listen{HTTPS: 18443}, AuthorizeCGI: "/webman/3rdparty/App/sso/authorize.cgi", OIDC: &OIDCConfig{NativeLogin: true}}
		entry := entryHandlerWithExternal(cfg, nil, nil, nil, func() External { return e })
		req := httptest.NewRequest("GET", "https://dsm.example.com/webman/3rdparty/App/sso/open.cgi?launchParam="+url.QueryEscape(launcher.Query().Get("launchParam")), nil)
		w := httptest.NewRecorder()
		entry.ServeHTTP(w, req)
		back, _ := url.Parse(w.Header().Get("Location"))
		if w.Code != 302 || back.Query().Get("resume") != resume || back.Query().Get("return") != origin+ReturnPath || back.Query().Get("login_attempt") != "1" {
			t.Fatal(w.Code, back)
		}
		cgi.Verifier = &fakeDSM{user: "alice", token: "tok", addr: "192.0.2.7"}
		done := cgiPost(t, cgi, "?"+back.RawQuery, "tok")
		final := b.do("GET", done.Header().Get("Location"), navigate...)
		if final.Code != 302 || !strings.HasPrefix(final.Header().Get("Location"), origin+"/accounts/oidc/dsm/login/callback/") {
			t.Fatal(final.Code, final.Body, final.Header())
		}
	}
}

func TestExternalResumeRevisionChangesFallbackAtBothConsumers(t *testing.T) {
	initial := External{Accept: []string{"https://manual.example.com", "https://connect.example.com"}, R1: 8, R2: 16}
	for _, current := range []External{
		{Accept: []string{"https://connect.example.com"}, R1: 9, R2: 16},                               // removed manual
		{Accept: []string{"https://connect.example.com", "https://manual.example.com"}, R1: 9, R2: 17}, // reordered
		{Accept: initial.Accept, R1: 10, R2: 16},                                                       // manual changed and restored
		{Accept: initial.Accept, R1: 8, R2: 18},                                                        // Connect revoked and restored
		{},
	} {
		raw := externalResume("txn", 1, initial)
		cfg := Config{Package: "App", Mode: ModeOIDC, NativeIngress: true, Listen: Listen{HTTPS: 18443}, OIDC: &OIDCConfig{NativeLogin: true}, AuthorizeCGI: "/authorize.cgi"}
		entry := entryHandlerWithExternal(cfg, nil, nil, nil, func() External { return current })
		w := httptest.NewRecorder()
		entry.ServeHTTP(w, httptest.NewRequest("GET", "https://dsm.example.com:5001/open.cgi?ssoResume="+url.QueryEscape(raw), nil))
		if w.Code != 302 || w.Header().Get("Location") != "https://dsm.example.com:18443/" {
			t.Fatal("entry stale", current, w.Code, w.Header())
		}
		cgi := newCGI(&fakeDSM{})
		cgi.External = func() External { return current }
		cgi.AppHTTPSPort = 18443
		w = httptest.NewRecorder()
		cgi.ServeHTTP(w, httptest.NewRequest("GET", cgiURL+"?resume="+url.QueryEscape(raw), nil))
		if w.Code != 302 || w.Header().Get("Location") != "https://nas.example:18443/" {
			t.Fatal("CGI stale", current, w.Code, w.Header())
		}
	}
}

func TestExternalResumeOnlyAndMalformedFallback(t *testing.T) {
	e := External{Accept: []string{"https://app.example.com"}, R1: 1, R2: 2}
	h := newCGI(&fakeDSM{})
	h.External = func() External { return e }
	for _, raw := range []string{"txn:x0:1.2", "txn:x0:0001.02"} {
		req := httptest.NewRequest("GET", cgiURL+"?resume="+url.QueryEscape(raw), nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 || !strings.Contains(w.Body.String(), url.QueryEscape(raw)) {
			t.Fatal("not preserved", raw, w.Code, w.Body)
		}
	}
	for _, raw := range []string{"txn:x1:1.2", "txn:x2:1.2", "txn:x0:1e3.2", "txn:x0:1.3", "txn:x0:18446744073709551615.2"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", cgiURL+"?resume="+url.QueryEscape(raw), nil))
		if w.Code != 302 || w.Header().Get("Location") != "https://nas.example:18443/" {
			t.Fatal(raw, w.Code, w.Header())
		}
	}
}

func TestPublicExplanationHasNoHostnamesAndUsesNineLocales(t *testing.T) {
	e := External{Accept: []string{"https://private-app.example.com"}}
	f, _ := newFront(t, ModeHeader, newUpstream(t, nil), func(c *FrontConfig) { c.External = func() External { return e } })
	for _, locale := range denialLocales {
		w := newBrowser(t, f).do("GET", e.Accept[0]+"/", append(navigate, "Accept-Language", locale)...)
		body := w.Body.String()
		if w.Code != 503 || !strings.Contains(body, "dsm_origin_missing") || strings.Contains(body, "example.com") || strings.Contains(body, "https://") || strings.Contains(body, "webman") || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Content-Language") != locale {
			t.Fatal(locale, w.Code, body, w.Header())
		}
	}
}

func TestExternalCGIReturnAllowlistAndOriginChecks(t *testing.T) {
	e := External{Accept: []string{"https://app.example.com"}}
	h := newCGI(&fakeDSM{})
	h.External = func() External { return e }
	for _, ret := range []string{"https://evil.example.com" + ReturnPath, "https://app.example.com:444" + ReturnPath, "https://app.example.com" + ReturnPath + "?", "https://app.example.com/%5fsso/dsm-return"} {
		w := cgiPost(t, h, cgiQuery(ret), "")
		if w.Code != 400 {
			t.Fatal(ret, w.Code, w.Header())
		}
	}
	req := httptest.NewRequest("POST", cgiURL+cgiQuery(e.Accept[0]+ReturnPath), strings.NewReader("SynoToken=tok"))
	req.Header.Set("Origin", e.Accept[0])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code, w.Body)
	}
}
