package idgateway

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func externalIngress(t *testing.T, mode Mode, external func() External) (*Front, http.Handler) {
	t.Helper()
	f, _ := newFront(t, mode, newUpstream(t, nil), func(c *FrontConfig) {
		c.External = external
		if mode == ModeOIDC {
			c.BrowserRedirect = &BrowserRedirectConfig{}
			c.PublicListen = Listen{HTTPS: 18443}
		}
	})
	ingress := nativeIngressHandler(Config{Package: "App", Listen: Listen{HTTPS: 18443}}, f)
	return f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
		r.Header.Set(nativeClientIPHeader, "192.0.2.7")
		ingress.ServeHTTP(w, r)
	})
}
func TestExternalAuthorityRequiresExactCanonicalOrigin(t *testing.T) {
	for _, mode := range []Mode{ModeHeader, ModeNone, ModeOIDC} {
		e := External{Accept: []string{"https://app.example.com"}}
		_, h := externalIngress(t, mode, func() External { return e })
		for _, origin := range []string{"https://app.example.com", "https://app.example.com:443", "https://nas.example.com:18443"} {
			w := newBrowser(t, h).do("GET", origin+"/", navigate...)
			if w.Code == 400 {
				t.Fatal(mode, origin, w.Body)
			}
			for _, cookie := range w.Result().Cookies() {
				if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" {
					t.Fatal(cookie)
				}
			}
		}
		for _, origin := range []string{"https://evil.example.com", "https://app.example.com.evil.example.com", "https://app.example.com:444"} {
			w := newBrowser(t, h).do("GET", origin+"/", "X-Forwarded-Host", "app.example.com", "Forwarded", "host=app.example.com")
			if w.Code != 400 {
				t.Fatal(mode, origin, w.Code, w.Body)
			}
		}
		e = External{}
		if w := newBrowser(t, h).do("GET", "https://app.example.com/"); w.Code != 400 {
			t.Fatal("revoked authority accepted", w.Code)
		}
	}
}
func TestExternalAuthorityDoesNotRelaxCSRFOrMutualTLS(t *testing.T) {
	f, h := externalIngress(t, ModeHeader, func() External { return External{Accept: []string{"https://app.example.com"}} })
	for _, hdr := range [][]string{{"Origin", "https://evil.example.com"}, {"Origin", "https://app.example.com:18443"}, {"Upgrade", "websocket", "Connection", "Upgrade"}} {
		w := newBrowser(t, h).do("POST", "https://app.example.com/update", hdr...)
		if w.Code != 403 {
			t.Fatal(hdr, w.Code, w.Body)
		}
	}
	private := nativeIngressHandler(Config{Package: "App", Listen: Listen{HTTPS: 18443}}, f)
	w := httptest.NewRecorder()
	private.ServeHTTP(w, httptest.NewRequest("GET", "https://app.example.com/", nil))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "authenticated DSM ingress") {
		t.Fatal(w.Code, w.Body)
	}
}
