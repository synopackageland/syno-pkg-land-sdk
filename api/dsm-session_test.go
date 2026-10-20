package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDSMSessionRequiresRequestCookieAndToken(t *testing.T) {
	t.Setenv("HTTP_X_SYNO_TOKEN", "environment-token")
	for _, tc := range []struct {
		name    string
		headers map[string][]string
		cookies []string
		pass    bool
	}{
		{"x header", map[string][]string{"X-SYNO-TOKEN": {"request-token"}}, []string{"session"}, true},
		{"SynoToken", map[string][]string{"SynoToken": {"request-token"}}, []string{"session"}, true},
		{"same aliases", map[string][]string{"X-SYNO-TOKEN": {"request-token"}, "SynoToken": {"request-token"}}, []string{"session"}, true},
		{"no token", nil, []string{"session"}, false},
		{"no cookie", map[string][]string{"X-SYNO-TOKEN": {"request-token"}}, nil, false},
		{"duplicate cookie", map[string][]string{"X-SYNO-TOKEN": {"request-token"}}, []string{"a", "b"}, false},
		{"duplicate header", map[string][]string{"X-SYNO-TOKEN": {"a", "b"}}, []string{"session"}, false},
		{"conflicting aliases", map[string][]string{"X-SYNO-TOKEN": {"a"}, "SynoToken": {"b"}}, []string{"session"}, false},
		{"oversize", map[string][]string{"X-SYNO-TOKEN": {strings.Repeat("a", 4097)}}, []string{"session"}, false},
		{"control characters", map[string][]string{"X-SYNO-TOKEN": {"secret\r\nInjected: value"}}, []string{"session"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api.cgi?SynoToken=query-token", nil)
			for k, values := range tc.headers {
				for _, v := range values {
					r.Header.Add(k, v)
				}
			}
			for _, v := range tc.cookies {
				r.AddCookie(&http.Cookie{Name: "id", Value: v})
			}
			got, err := DSMSession(r)
			if (err == nil) != tc.pass {
				t.Fatal("unexpected acceptance", err)
			}
			if tc.pass && (got["dsm_cookie"] != "session" || got["dsm_synotoken"] != "request-token" || got["remote_addr"] != "192.0.2.1" || len(got) != 3) {
				t.Fatal("wrong request credentials")
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("credential leak")
			}
		})
	}
}

// A signed-in browser whose page holds no SynoToken (a restored DSM session,
// an Application Portal page) is not a temporary failure, and an expired
// session is a different one: the code says which.
func TestDSMSessionNamesAMissingToken(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string][]string
		cookie  bool
		code    string
	}{
		{"no token", nil, true, "credential_not_ready"},
		{"empty token", map[string][]string{"X-SYNO-TOKEN": {""}}, true, "credential_not_ready"},
		{"no cookie", map[string][]string{"X-SYNO-TOKEN": {"request-token"}}, false, "session_required"},
		{"neither", nil, false, "session_required"},
		{"control characters", map[string][]string{"X-SYNO-TOKEN": {"a\x01"}}, true, "session_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api.cgi", nil)
			for k, values := range tc.headers {
				for _, v := range values {
					r.Header.Add(k, v)
				}
			}
			if tc.cookie {
				r.AddCookie(&http.Cookie{Name: "id", Value: "session"})
			}
			_, err := DSMSession(r)
			var e *Error
			if !errors.As(err, &e) || e.Code != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
		})
	}
}

// The executor asks DSM's App Privileges about the browser's address; without
// one it refuses, so DSMSession forwards only an address it could parse.
func TestDSMSessionForwardsTheBrowserAddress(t *testing.T) {
	for remote, want := range map[string]string{"[2001:db8::7]:443": "2001:db8::7", "198.51.100.4:1": "198.51.100.4", "": "", "not-an-ip:1": ""} {
		r := httptest.NewRequest("POST", "/api.cgi", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-SYNO-TOKEN", "request-token")
		r.AddCookie(&http.Cookie{Name: "id", Value: "session"})
		got, err := DSMSession(r)
		if err != nil {
			t.Fatal(err)
		}
		if addr, ok := got["remote_addr"]; addr != want || ok != (want != "") {
			t.Fatalf("%q forwarded as %q", remote, addr)
		}
	}
}
