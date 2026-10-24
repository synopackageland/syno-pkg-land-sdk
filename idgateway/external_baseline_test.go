package idgateway

import (
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

// Volatile transaction IDs are the only normalization: browser cookies are
// preseeded equally so response code, Location and Set-Cookie are comparable.
func baselineResponse(w *httptest.ResponseRecorder) (int, string, []string, string) {
	location := w.Header().Get("Location")
	if u, err := url.Parse(location); err == nil && u.Query().Has("txn") {
		q := u.Query()
		q.Set("txn", "TXN")
		u.RawQuery = q.Encode()
		location = u.String()
	}
	body := w.Body.String()
	if w.Code == 302 {
		body = ""
	} // net/http embeds the same volatile Location in HTML.
	return w.Code, location, w.Header().Values("Set-Cookie"), body
}
func TestExternalAbsentEqualsBaseline(t *testing.T) {
	for _, mode := range []Mode{ModeHeader, ModeNone, ModeOIDC} {
		for _, none := range []bool{false, true} {
			for _, scheme := range []string{"http", "https"} {
				cfg := Config{Package: "App", StateDir: t.TempDir(), Listen: Listen{HTTPS: 18443}}
				if none {
					cfg.External = &ExternalConfig{Connect: "none"}
				}
				reader := NewExternalReader(cfg)
				reader.paths = []string{t.TempDir() + "/absent.json"}
				upstream := newUpstream(t, nil)
				base, _ := newFront(t, mode, upstream, nil)
				current, _ := newFront(t, mode, upstream, func(c *FrontConfig) { c.External = reader.Read })
				for _, tc := range []struct {
					method, path string
					headers      []string
				}{
					{"GET", "/", navigate},
					{"POST", "/api", []string{"Origin", scheme + "://nas.example:18443", "Accept", "application/json"}},
					{"GET", "/_sso/unknown", nil},
					{"GET", "/share/file", nil},
					{"GET", ReturnPath + "?txn=unknown", nil},
				} {
					b, c := newBrowser(t, base), newBrowser(t, current)
					b.cookies[base.cookie+"_b"] = "fixed_browser"
					c.cookies[current.cookie+"_b"] = "fixed_browser"
					x := b.do(tc.method, scheme+"://nas.example:18443"+tc.path, tc.headers...)
					y := c.do(tc.method, scheme+"://nas.example:18443"+tc.path, tc.headers...)
					a1, a2, a3, a4 := baselineResponse(x)
					b1, b2, b3, b4 := baselineResponse(y)
					if a1 != b1 || a2 != b2 || !reflect.DeepEqual(a3, b3) || a4 != b4 {
						t.Fatalf("%s none=%v %s %s: baseline=%d %s %v %q current=%d %s %v %q", mode, none, scheme, tc.path, a1, a2, a3, a4, b1, b2, b3, b4)
					}
				}
			}
		}
	}
}
