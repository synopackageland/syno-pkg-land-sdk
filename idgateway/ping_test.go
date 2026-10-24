package idgateway

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPingBeforeAuthorityAndAdmission(t *testing.T) {
	for _, mode := range []Mode{ModeHeader, ModeNone, ModeOIDC} {
		up := newUpstream(t, nil)
		f, _ := newFront(t, mode, up, func(c *FrontConfig) {
			if mode == ModeOIDC {
				c.BrowserRedirect = &BrowserRedirectConfig{}
				c.PublicListen = Listen{HTTPS: 18443}
			}
		})
		for _, native := range []bool{false, true} {
			var h http.Handler = f
			if native {
				h = nativeIngressHandler(Config{Package: "App", Listen: Listen{HTTPS: 18443}}, f)
			}
			for _, host := range []string{"localhost", "app.example.com", "app.example.com:443", "app.example.com:9999", "[2001:db8::1]:443", "bad/authority", ""} {
				r := httptest.NewRequest("GET", "https://example.com"+PingPath, nil)
				r.Host = host
				r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != 200 || w.Body.String() != `{"package":"App"}` || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("mode=%s native=%v host=%q: %d %s %v", mode, native, host, w.Code, w.Body, w.Header())
				}
			}
		}
		if up.hits != 0 {
			t.Fatal("ping reached upstream")
		}
	}
}

func TestPingMethodsAndExactPath(t *testing.T) {
	f, _ := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	b := newBrowser(t, f)
	if w := b.do("HEAD", "https://example.com"+PingPath); w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "17" {
		t.Fatal(w.Code, w.Body, w.Header())
	}
	for _, method := range []string{"POST", "PUT", "DELETE", "OPTIONS"} {
		if w := b.do(method, "https://example.com"+PingPath); w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatal(method, w.Code, w.Header())
		}
	}
	for _, path := range []string{"/", "/_spl/ping/", "/_spl/ping-other", "/_sso/unknown"} {
		w := b.do("GET", "https://example.com"+path, navigate...)
		if w.Body.String() == `{"package":"App"}` || w.Code == 200 {
			t.Fatal("ping changed another route", path, w.Code)
		}
	}
	h := nativeIngressHandler(Config{Package: "App"}, f)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://example.com"+PingPath, nil))
	if w.Code != 403 {
		t.Fatal("ping bypassed mutual TLS", w.Code)
	}
}
