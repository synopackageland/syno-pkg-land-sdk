package api

import (
	"net/http/httptest"
	"testing"
)

func TestReviewInvalidDuplicateIDMustNotBeDiscarded(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Cookie", "id=session; id=invalid\tvalue")
	r.Header.Set("X-SYNO-TOKEN", "token")
	if _, err := DSMSession(r); err == nil {
		t.Fatal("duplicate id silently dropped; accepted request")
	}
}
func TestCookieRejectsEveryASCIIControl(t *testing.T) {
	for c := 0; c <= 127; c++ {
		if c >= 32 && c != 127 {
			continue
		}
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("Cookie", "id=sess"+string(rune(c))+"ion")
		r.Header.Set("X-SYNO-TOKEN", "token")
		if _, err := DSMSession(r); err == nil {
			t.Errorf("accepted control %d", c)
		}
	}
}
