package idgateway

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClasslessEntryResumesColdLoginForEveryGatewayMode(t *testing.T) {
	for _, mode := range []Mode{ModeNone, ModeHeader, ModeOIDC} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := Config{Package: "Reader", Mode: mode, Listen: Listen{HTTPS: 28443}, AuthorizeCGI: "/webman/3rdparty/Reader/sso/authorize.cgi"}
			w := httptest.NewRecorder()
			entryHandler(cfg, func() error { return nil }).ServeHTTP(w, httptest.NewRequest("GET", "https://nas.example:5001/webman/3rdparty/Reader/sso/open.cgi?ssoResume=opaque_txn%3A28443", nil))
			u, _ := url.Parse(w.Header().Get("Location"))
			if w.Code != 302 || u.Path != cfg.AuthorizeCGI || u.Query().Get("txn") != "opaque_txn" || u.Query().Get("login_attempt") != "1" {
				t.Fatalf("resume: %d %s", w.Code, u)
			}
		})
	}
}

func TestStoppedGatewayNeverClaimsMissingDSMSession(t *testing.T) {
	h := newCGI(&fakeDSM{})
	h.Available = func() bool { return false }
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", cgiURL+cgiQuery("https://nas.example:18443/_sso/dsm-return"), nil)
	r.Header.Set("Accept-Language", "zh-TW")
	h.ServeHTTP(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "套件中心") || strings.Contains(w.Body.String(), "sign_in_required") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
