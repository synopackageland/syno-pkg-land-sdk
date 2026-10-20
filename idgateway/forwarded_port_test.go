package idgateway

import (
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
)

func TestCanonicalForwardedPortDefaultsAndIPv6(t *testing.T) {
	up, _ := url.Parse("http://127.0.0.1:8080")
	f := &Front{upstream: up}
	for _, tc := range []struct{ raw, port string }{{"http://nas.example/x", "80"}, {"https://nas.example/x", "443"}, {"https://[::1]:28443/x", "28443"}} {
		r := httptest.NewRequest("GET", tc.raw, nil)
		r.Header.Set("X-Forwarded-Port", "65535")
		out := r.Clone(r.Context())
		f.rewrite(&httputil.ProxyRequest{In: r, Out: out})
		if got := out.Header.Get("X-Forwarded-Port"); got != tc.port {
			t.Fatalf("%s port=%q", tc.raw, got)
		}
	}
}
