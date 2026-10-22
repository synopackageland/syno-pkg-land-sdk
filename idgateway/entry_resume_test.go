package idgateway

import (
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestClasslessOIDCEntryResumesOnlyDeclaredTransaction(t *testing.T) {
	cfg := Config{LaunchPath: "/#/native-sso?organization=fixed", Package: "Reader", Mode: ModeOIDC, NativeIngress: true, ContainerApp: true, Listen: Listen{HTTPS: 28443}, OIDC: &OIDCConfig{NativeLogin: true}, AuthorizeCGI: "/webman/3rdparty/Reader/sso/authorize.cgi"}
	h := entryHandler(cfg, func() error { return nil })
	r := httptest.NewRequest("GET", "https://nas.example:5001/webman/3rdparty/Reader/sso/open.cgi?ssoResume=opaque_txn%3A28443", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Port() != "5001" || u.Path != cfg.AuthorizeCGI || u.Query().Get("txn") != "opaque_txn" || u.Query().Get("login_attempt") != "1" || u.Query().Get("return") != "https://nas.example:28443/_sso/dsm-return" {
		t.Fatalf("lost classless resume: %s", u)
	}
	// DSM URL launchers preserve the launch parameter as one nested value.
	r.URL.RawQuery = "launchParam=" + url.QueryEscape("ssoResume=opaque_txn%3A28443")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	u, _ = url.Parse(w.Header().Get("Location"))
	if u.Query().Get("txn") != "opaque_txn" {
		t.Fatal("nested DSM launchParam lost transaction")
	}
	r.URL.RawQuery = "launchParam=" + url.QueryEscape(`{"ssoResume":"opaque_txn:28443"}`)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	u, _ = url.Parse(w.Header().Get("Location"))
	if u.Query().Get("txn") != "opaque_txn" {
		t.Fatal("DSM JSON launchParam lost transaction")
	}
	indexed := url.Values{}
	for n, ch := range []byte(`{"ssoResume":"opaque_txn:28443"}`) {
		indexed.Set(strconv.Itoa(n), string(ch))
	}
	r.URL.RawQuery = "launchParam=" + url.QueryEscape(indexed.Encode())
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	u, _ = url.Parse(w.Header().Get("Location"))
	if u.Query().Get("txn") != "opaque_txn" {
		t.Fatal("DSM URL launcher character-indexed string lost transaction")
	}
	for _, value := range []string{"//evil.example/", "txn:5001", "txn:28443:1", "../x:28443"} {
		r.URL.RawQuery = "ssoResume=" + url.QueryEscape(value)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 || strings.Contains(w.Header().Get("Location"), "evil") {
			t.Fatalf("invalid resume accepted: %s %d", value, w.Code)
		}
	}
}
