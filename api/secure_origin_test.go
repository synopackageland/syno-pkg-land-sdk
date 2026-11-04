package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireSecureOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, origin, referer string
		allowed               bool
	}{
		{"HTTPS origin", "https://example.test:7443", "", true},
		{"HTTPS referer", "", "https://example.test/app/", true},
		{"HTTP origin", "http://example.test:5000", "", false},
		{"origin wins", "http://example.test", "https://example.test/", false},
		{"opaque origin", "null", "https://example.test/", false},
		{"no source", "", "", false},
		{"missing host", "https:", "", false},
		{"credentials", "https://user:secret@example.test", "", false},
		{"malformed", "https://[", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "https://example.test/api.cgi", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Referer", tc.referer)
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			err := RequireSecureOrigin(r)
			if tc.allowed {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Code != "https_required" {
				t.Fatalf("got %v", err)
			}
		})
	}
}
