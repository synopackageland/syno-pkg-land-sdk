package main

import (
	"context"
	"testing"
)

func TestReviewCookieControlRejectedBeforeDSM(t *testing.T) {
	for _, cookie := range []string{"sess\tion", "sess\x7fion", "sess\x01ion"} {
		e, _, _ := setup(t)
		if _, code := e.verified(context.Background(), Auth{Cookie: cookie, SynoToken: "token"}); code == "" {
			t.Errorf("cookie control character accepted: %q", cookie)
		}
	}
}

func TestExecutorCookieRejectsEveryASCIIControl(t *testing.T) {
	for c := 0; c <= 127; c++ {
		if c >= 32 && c != 127 {
			continue
		}
		e, _, _ := setup(t)
		if _, code := e.verified(context.Background(), Auth{Cookie: "sess" + string(rune(c)) + "ion", SynoToken: "token"}); code != "AUTH_DENIED" {
			t.Errorf("accepted control %d", c)
		}
	}
}
