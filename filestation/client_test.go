package filestation

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func sessionRequest() *http.Request {
	r := httptest.NewRequest("POST", "http://package/api", nil)
	r.AddCookie(&http.Cookie{Name: "id", Value: "test-session"})
	r.Header.Set("X-SYNO-TOKEN", "test-token")
	r.Header.Set("Origin", "http://package")
	return r
}
func TestLegacyClientStillRejectsRemoteDestinations(t *testing.T) {
	for _, base := range []string{"https://127.0.0.1:5001", "http://example.com", "http://localhost:5000", "http://127.0.0.1/path", "http://u@127.0.0.1"} {
		if _, err := NewClient(base); err == nil {
			t.Fatalf("accepted %s", base)
		}
	}
}
