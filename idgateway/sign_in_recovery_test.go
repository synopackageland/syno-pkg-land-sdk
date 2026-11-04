package idgateway

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSignInMismatchRecoversOnceEvenWhenCookiesAreDropped(t *testing.T) {
	f, clock := newFront(t, ModeHeader, newUpstream(t, nil), func(c *FrontConfig) { c.IngressPath = "/reader/" })
	b := newBrowser(t, f)
	ret := dsmBounce(t, b.do("GET", "http://gateway.invalid/reader/deep", navigate...), alice, clock.t)
	clear(b.cookies)
	restart := b.do("GET", ret, navigate...)
	if restart.Code != http.StatusFound {
		t.Fatalf("mismatch did not self-heal: %d %s", restart.Code, restart.Body)
	}
	newReturn := dsmBounce(t, restart, alice, clock.t)
	if newReturn == ret {
		t.Fatal("recovery reused the broken transaction")
	}
	clear(b.cookies)
	failed := b.do("GET", newReturn, navigate...)
	assertDenial(t, failed, 400, "dsm_sign_in_expired", "sign_in_expired", "cookies", true)
	if strings.Contains(failed.Body.String(), "assertion=") || strings.Contains(failed.Body.String(), "txn=") {
		t.Fatal("failure leaked callback credentials")
	}
	// Reloading the failed callback must keep the loop guard without cookies.
	clear(b.cookies)
	failed = b.do("GET", newReturn, navigate...)
	if failed.Code != 400 || failed.Header().Get("Location") != "" {
		t.Fatal("reload re-entered automatic sign-in")
	}
	// The visible sign-in link creates a fresh attempt without the old query.
	_, link, ok := strings.Cut(failed.Body.String(), `data-i18n="gateway.sign_in" href="`)
	if !ok {
		t.Fatal("missing manual recovery link")
	}
	link, _, _ = strings.Cut(link, `"`)
	launcher, err := url.Parse(html.UnescapeString(link))
	if err != nil || launcher.Scheme != "http" || launcher.Query().Get("launchApp") == "" {
		t.Fatal("invalid manual recovery link")
	}
	var launch map[string]string
	if err := json.Unmarshal([]byte(launcher.Query().Get("launchParam")), &launch); err != nil {
		t.Fatal(err)
	}
	resume, ok := parseResume(launch["ssoResume"])
	if !ok || resume.Txn == "" || strings.HasPrefix(resume.Txn, recoveryTxnPrefix) {
		t.Fatal("explicit retry did not start a new attempt")
	}
}

func TestExpiredOIDCTransactionRecoveryKeepsClientAndPKCE(t *testing.T) {
	f, clock := newFront(t, ModeOIDC, newUpstream(t, nil), nil)
	b := newBrowser(t, f)
	callback := origin + "/accounts/oidc/dsm/login/callback/"
	start := origin + "/_sso/authorize?" + authQuery("paperless", callback, "v").Encode()
	ret := dsmBounce(t, b.do("GET", start, navigate...), alice, clock.t)
	clock.t = clock.t.Add(TxnTTL + time.Second)
	restarted := b.do("GET", ret, navigate...)
	ret = dsmBounce(t, restarted, alice, clock.t)
	done := b.do("GET", ret, navigate...)
	if done.Code != 302 || !strings.HasPrefix(done.Header().Get("Location"), callback) {
		t.Fatal("OIDC recovery lost its client callback")
	}
	code := codeFrom(t, done.Header().Get("Location"))
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {callback}, "client_id": {"paperless"}, "client_secret": {"s3cret"}, "code_verifier": {"wrong"}}
	if res := exchange(f.cfg.Provider, form); res.Code != 400 {
		t.Fatal("recovery bypassed PKCE")
	}
}

func TestSignInMismatchFreshBrowserCanRecoverWithoutRedeemingForeignAssertion(t *testing.T) {
	f, clock := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	owner := newBrowser(t, f)
	ret := dsmBounce(t, owner.do("GET", "https://gateway.invalid:18443/private?secret=private", navigate...), alice, clock.t)
	stranger := newBrowser(t, f)
	restarted := stranger.do("GET", ret, navigate...)
	if restarted.Code != 302 || len(f.sessions) != 0 || strings.Contains(restarted.Header().Get("Location"), "private") {
		t.Fatal("foreign callback was redeemed or disclosed its target")
	}
	if own := owner.do("GET", ret); own.Code != 302 || own.Header().Get("Location") != "/private?secret=private" {
		t.Fatal("recovery burned another browser's transaction")
	}
	newReturn := dsmBounce(t, restarted, bob, clock.t)
	if own := stranger.do("GET", newReturn, navigate...); own.Code != 302 || own.Header().Get("Location") != "/" {
		t.Fatal("fresh sign-in did not use the fresh subject and safe App root")
	}
}

func TestInvalidAssertionRecoveryIsBounded(t *testing.T) {
	f, clock := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	b := newBrowser(t, f)
	ret := dsmBounce(t, b.do("GET", "https://gateway.invalid:18443/deep?x=1", navigate...), alice, clock.t)
	invalid := func(raw string) string {
		u, _ := url.Parse(raw)
		q := u.Query()
		q.Set("assertion", "invalid")
		u.RawQuery = q.Encode()
		return u.String()
	}
	restart := b.do("GET", invalid(ret), navigate...)
	if restart.Code != 302 {
		t.Fatal("invalid assertion did not recover", restart.Code)
	}
	ret = dsmBounce(t, restart, alice, clock.t)
	failed := b.do("GET", invalid(ret), navigate...)
	assertDenial(t, failed, 400, "dsm_assertion_invalid", "assertion_invalid", "verified", true)
	if len(f.sessions) != 0 {
		t.Fatal("invalid assertion established a session")
	}
}

func TestExpiredTransactionRecoveryKeepsBoundTarget(t *testing.T) {
	f, clock := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	b := newBrowser(t, f)
	ret := dsmBounce(t, b.do("GET", "https://gateway.invalid:18443/deep?x=1", navigate...), alice, clock.t)
	clock.t = clock.t.Add(TxnTTL + time.Second)
	restart := b.do("GET", ret, navigate...)
	if restart.Code != 302 {
		t.Fatal("expired transaction did not recover")
	}
	ret = dsmBounce(t, restart, alice, clock.t)
	if res := b.do("GET", ret, navigate...); res.Code != 302 || res.Header().Get("Location") != "/deep?x=1" {
		t.Fatal("recovery lost bound navigation")
	}
}
