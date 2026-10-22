package idgateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func TestBrowserRedirectPerRequest(t *testing.T) {
	const authQuery = "?client_id=app&state=unchanged&redirect_uri=https%3A%2F%2Fapp.example%3A8443%2Fcallback"
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", `"upstream"`)
		switch r.URL.Path {
		case "/location":
			w.Header().Set("Location", issuer+"/authorize"+authQuery)
			w.WriteHeader(302)
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"url": issuer + "/authorize" + authQuery})
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("ETag", `"old"`)
			io.WriteString(w, `<script>location=decodeURIComponent("http%3A//127.0.0.1%3A18090/authorize%3Fclient_id%3Dapp%26state%3Dunchanged")</script>`)
		}
	})
	f, _ := newFront(t, ModeOIDC, up, func(c *FrontConfig) {
		c.UpstreamAccess = "public"
		c.Access = "dsm-users"
		c.BrowserRedirect = &BrowserRedirectConfig{HTMLPaths: []string{"/redirect"}}
		c.PublicListen = Listen{HTTPS: 8443}
		c.RewriteJSON = []JSONRewrite{{Method: "GET", Path: "/json", Field: "url"}}
	})
	// One provider/discovery and gateway installation, two independent authorities.
	for _, host := range []string{"192.0.2.10", "fresh-alias.example", "[2001:db8::10]"} {
		origin := "https://" + host + ":8443"
		for _, path := range []string{"/location", "/json", "/redirect"} {
			r := httptest.NewRequest("GET", origin+path, nil)
			r.Header.Set("X-Forwarded-Host", "attacker.example:8443")
			r.Header.Set("X-Forwarded-Proto", "http")
			r.Header.Set("Forwarded", "host=attacker.example:9443;proto=http")
			w := httptest.NewRecorder()
			f.ServeHTTP(w, r)
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
				t.Fatalf("origin response cacheable: %s %v", path, w.Header())
			}
			switch path {
			case "/location":
				if got := w.Header().Get("Location"); got != origin+"/_sso/authorize"+authQuery {
					t.Fatalf("Location = %s", got)
				}
			case "/json":
				var v map[string]string
				json.Unmarshal(w.Body.Bytes(), &v)
				if v["url"] != origin+"/_sso/authorize"+authQuery {
					t.Fatal(v)
				}
			default:
				want := strings.ReplaceAll(url.QueryEscape(origin+"/_sso/authorize"), "%2F", "/") + "%3Fclient_id%3Dapp%26state%3Dunchanged"
				if !strings.Contains(w.Body.String(), want) || w.Header().Get("ETag") != "" || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("HTML %d: %s headers=%v", w.Code, w.Body.String(), w.Header())
				}
			}
		}
	}
	hits := up.hits
	for _, origin := range []string{"https://app.example:9443", "http://app.example:8443"} {
		for _, path := range []string{"/redirect", "/_sso/authorize"} {
			w := httptest.NewRecorder()
			f.ServeHTTP(w, httptest.NewRequest("GET", origin+path, nil))
			if w.Code != 400 {
				t.Fatalf("%s%s admitted: %d", origin, path, w.Code)
			}
		}
	}
	if up.hits != hits {
		t.Fatal("invalid authority reached upstream")
	}
	d := f.cfg.Provider.discovery()
	if d["issuer"] != issuer || d["token_endpoint"] != issuer+"/token" || d["authorization_endpoint"] != issuer+"/authorize" {
		t.Fatal(d)
	}
}

func TestBrowserRedirectBoundedHTML(t *testing.T) {
	for _, tc := range []struct {
		name, body, typ, encoding string
		status                    int
		want                      string
		fail                      bool
	}{
		{name: "raw", body: issuer + `/authorize?state=a&amp;nonce=b`, want: `https://nas.example:8443/_sso/authorize?state=a&amp;nonce=b`},
		{name: "escaped", body: strings.ReplaceAll(issuer+`/authorize?state=a`, "/", `\/`), want: `https:\/\/nas.example:8443\/_sso\/authorize?state=a`},
		{name: "encoded", body: url.QueryEscape(issuer + `/authorize?state=a`), want: url.QueryEscape(`https://nas.example:8443/_sso/authorize?state=a`)},
		{name: "lookalikes", body: issuer + `/authorize.evil?state=a ` + issuer + `/authorize/extra ` + issuer + `/token?x=a`, want: issuer + `/authorize.evil?state=a ` + issuer + `/authorize/extra ` + issuer + `/token?x=a`},
		{name: "compressed", encoding: "gzip", body: "compressed", fail: true},
		{name: "oversized", body: strings.Repeat("x", maxRewriteBody+1), fail: true},
		{name: "nonhtml", typ: "application/json", body: `{}`, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get("If-None-Match") != "" || r.Header.Get("Range") != "" {
					t.Error("rewrite transport not bounded")
				}
				typ := tc.typ
				if typ == "" {
					typ = "text/html"
				}
				w.Header().Set("Content-Type", typ)
				if tc.encoding != "" {
					w.Header().Set("Content-Encoding", tc.encoding)
				}
				w.Header().Set("Content-Security-Policy", "script-src 'self'")
				io.WriteString(w, tc.body+`"`)
			})
			f, _ := newFront(t, ModeOIDC, up, func(c *FrontConfig) {
				c.UpstreamAccess = "public"
				c.Access = "dsm-users"
				c.PublicListen = Listen{HTTPS: 8443}
				c.BrowserRedirect = &BrowserRedirectConfig{HTMLPaths: []string{"/redirect"}}
			})
			w := newBrowser(t, f).do("GET", "https://nas.example:8443/redirect", "If-None-Match", `"old"`, "Range", "bytes=0-20")
			if tc.fail {
				if w.Code != 502 {
					t.Fatalf("unsafe HTML %d", w.Code)
				}
				return
			}
			if w.Code != 200 || w.Body.String() != tc.want+`"` || w.Header().Get("Content-Security-Policy") != "script-src 'self'" {
				t.Fatalf("rewrite %d %s", w.Code, w.Body.String())
			}
			// Outside the explicit path, upstream HTML stays byte-for-byte unchanged.
			r := httptest.NewRequest("GET", "https://nas.example:8443/other", nil)
			r.Header.Set("Accept-Encoding", "identity")
			w = httptest.NewRecorder()
			f.ServeHTTP(w, r)
			if !bytes.Equal(w.Body.Bytes(), []byte(tc.body+`"`)) {
				t.Fatal("undeclared HTML modified")
			}
		})
	}
}

func TestBrowserRedirectRequestHostsKeepIssuerAndPKCE(t *testing.T) {
	g := providerOnlyFront(t, newUpstream(t, nil))
	g.Front.cfg.BrowserRedirect = &BrowserRedirectConfig{}
	g.Front.cfg.PublicListen = Listen{HTTPS: 18443}
	for _, host := range []string{"192.0.2.10", "nas.example"} {
		origin := "https://" + host + ":18443"
		b := newBrowser(t, g.Front)
		start := b.do("GET", origin+"/_sso/authorize?"+authQuery("reader", origin+"/cb", "v").Encode(), navigate...)
		h := newCGI(&fakeDSM{})
		h.Verifier = verifierFunc(func(*http.Request) (api.Identity, error) { return api.Identity{User: "alice"}, nil })
		r := httptest.NewRequest("POST", start.Header().Get("Location"), strings.NewReader("SynoToken=tok"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		cgi := httptest.NewRecorder()
		h.ServeHTTP(cgi, r)
		if cgi.Code != 302 {
			t.Fatalf("CGI: %d %s", cgi.Code, cgi.Body)
		}
		ret := cgi.Header().Get("Location")
		other := newBrowser(t, g.Front)
		if w := other.do("GET", ret, navigate...); w.Code != 400 {
			t.Fatalf("other browser redeemed: %d", w.Code)
		}
		// Even copying the binding cookie cannot replay a transaction on another host.
		foreign := strings.Replace(ret, origin, "https://foreign-alias.example:18443", 1)
		if w := b.do("GET", foreign, navigate...); w.Code != 400 {
			t.Fatalf("other origin redeemed: %d", w.Code)
		}
		w := b.do("GET", ret, navigate...)
		if w.Code != 302 {
			t.Fatalf("callback %d %s", w.Code, w.Body)
		}
		form := url.Values{"grant_type": {"authorization_code"}, "code": {codeFrom(t, w.Header().Get("Location"))}, "redirect_uri": {origin + "/cb"}, "client_id": {"reader"}, "client_secret": {g.Secrets["reader"]}, "code_verifier": {"v"}}
		token := exchange(g.Provider, form)
		if token.Code != 200 {
			t.Fatalf("token %d %s", token.Code, token.Body)
		}
		var doc map[string]any
		json.Unmarshal(token.Body.Bytes(), &doc)
		payload, _ := base64.RawURLEncoding.DecodeString(strings.Split(doc["id_token"].(string), ".")[1])
		var claims map[string]any
		json.Unmarshal(payload, &claims)
		if claims["iss"] != issuer {
			t.Fatal("issuer changed", claims)
		}
		if exchange(g.Provider, form).Code != 400 {
			t.Fatal("code replay admitted")
		}
		// Another host is still not an allowed redirect on this origin.
		wrong := authQuery("reader", "https://other.example:18443/cb", "v")
		if _, err := g.Provider.Begin(origin, wrong); err == nil {
			t.Fatal("redirect weakened")
		}
	}
}

func TestBrowserRedirectConfigFailsClosed(t *testing.T) {
	for _, b := range []*BrowserRedirectConfig{
		{HTMLPaths: []string{"/_sso/authorize"}},
		{HTMLPaths: []string{"/%2e%2e/redirect"}},
	} {
		if b.validate() == nil {
			t.Fatalf("invalid config admitted: %#v", b)
		}
	}
	if err := (&BrowserRedirectConfig{}).validate(); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserRedirectSameOriginReferrerIsExplicitAndScoped(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Referrer-Policy", "no-referrer")
		io.WriteString(w, "<form method=post></form>")
	})
	for _, enabled := range []bool{false, true} {
		f, _ := newFront(t, ModeOIDC, up, func(c *FrontConfig) {
			c.Access = "dsm-users"
			c.UpstreamAccess = "public"
			c.PublicListen = Listen{HTTPS: 8443}
			c.BrowserRedirect = &BrowserRedirectConfig{HTMLPaths: []string{"/finish"}, SameOriginReferrer: enabled}
		})
		for _, path := range []string{"/finish", "/other"} {
			w := newBrowser(t, f).do("GET", "https://nas.example:8443"+path)
			want := "no-referrer"
			if enabled && path == "/finish" {
				want = "same-origin"
			}
			if w.Header().Get("Referrer-Policy") != want {
				t.Fatalf("enabled=%v path=%s policy=%s", enabled, path, w.Header().Get("Referrer-Policy"))
			}
		}
		// Restoring same-origin metadata never admits an opaque/null Origin.
		w := newBrowser(t, f).do("POST", "https://nas.example:8443/_sso/dsm-return", "Origin", "null")
		if w.Code != 403 {
			t.Fatal("null Origin admitted", w.Code)
		}
	}
}

func TestBrowserRedirectRejectsMalformedAuthority(t *testing.T) {
	up := newUpstream(t, nil)
	f, _ := newFront(t, ModeOIDC, up, func(c *FrontConfig) {
		c.BrowserRedirect = &BrowserRedirectConfig{}
		c.PublicListen = Listen{HTTPS: 8443}
	})
	for _, host := range []string{"bad_name:8443", "a..example:8443", "a.example.:8443", "user@nas.example:8443", "x\".example:8443", "nas.example:443"} {
		r := httptest.NewRequest("GET", "https://nas.example:8443/", nil)
		r.Host = host
		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("authority %q admitted: %d", host, w.Code)
		}
	}
}
