package idgateway

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const issuer = "http://127.0.0.1:18090"

func secretHash(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newProvider(t *testing.T) (*Provider, *clock) {
	t.Helper()
	key, err := LoadOrCreateSigningKey(filepath.Join(t.TempDir(), "k.pem"))
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{time.Unix(1_700_000_000, 0)}
	p, err := NewProvider(issuer, "nas.invalid", key, []Client{
		{ID: "paperless", SecretSHA256: secretHash("s3cret"), RedirectPaths: []string{"/accounts/oidc/dsm/login/callback/"}, RequirePKCE: true},
		{ID: "openlist", SecretSHA256: secretHash("ol"), RedirectPaths: []string{"/api/auth/sso_callback"}},
		{ID: "immich", SecretSHA256: secretHash("im"), RedirectPaths: []string{"/auth/login"}, NativeRedirects: []string{"app.immich:///oauth-callback"}, RequirePKCE: true, Roles: Roles{Claim: "immich_role", Admin: "admin", User: "user"}},
		{ID: "abs", SecretSHA256: secretHash("abs"), RedirectPaths: []string{"/auth/openid/callback"}, RequirePKCE: true, Roles: Roles{Claim: "groups", Admin: "admin", User: "user"}},
	}, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return p, c
}

const origin = "https://nas.example:18443"

func authQuery(client, redirect, verifier string, extra ...string) url.Values {
	q := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {redirect}, "scope": {"openid profile email"}, "state": {"st"}}
	if verifier != "" {
		q.Set("code_challenge", challenge(verifier))
		q.Set("code_challenge_method", "S256")
	}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	return q
}

var alice = Subject{UID: 1026, Name: "alice", Admin: true}
var bob = Subject{UID: 1027, Name: "bob"}

func codeFrom(t *testing.T, redirect string) string {
	t.Helper()
	u, err := url.Parse(redirect)
	if err != nil || u.Query().Get("state") != "st" || u.Query().Get("code") == "" {
		t.Fatalf("redirect %q", redirect)
	}
	return u.Query().Get("code")
}

func exchange(p *Provider, form url.Values, basic ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, issuer+"/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if len(basic) == 2 {
		r.SetBasicAuth(basic[0], basic[1])
	}
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	return w
}

func claimsOf(t *testing.T, jwt string) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c map[string]any
	if json.Unmarshal(raw, &c) != nil {
		t.Fatalf("jwt %q", jwt)
	}
	return c
}

func TestAuthorizationCodeFlowWithPKCE(t *testing.T) {
	p, _ := newProvider(t)
	redirect := origin + "/accounts/oidc/dsm/login/callback/"
	req, err := p.Begin(origin, authQuery("paperless", redirect, "verifier-1", "nonce", "n1"))
	if err != nil {
		t.Fatal(err)
	}
	code := codeFrom(t, p.Complete(req, alice))
	w := exchange(p, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {"verifier-1"}}, "paperless", "s3cret")
	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &tok) != nil || tok.TokenType != "Bearer" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	c := claimsOf(t, tok.IDToken)
	want := map[string]any{"iss": issuer, "aud": "paperless", "sub": "1026", "nonce": "n1", "preferred_username": "alice", "email": "alice@nas.invalid", "email_verified": true}
	for k, v := range want {
		if c[k] != v {
			t.Errorf("%s = %v, want %v", k, c[k], v)
		}
	}
	if g, _ := c["groups"].([]any); len(g) != 1 || g[0] != "admin" {
		t.Errorf("groups %v", c["groups"])
	}
	if c["exp"].(float64)-c["iat"].(float64) != IDTokenTTL.Seconds() {
		t.Errorf("lifetime %v", c)
	}
	r := httptest.NewRequest(http.MethodGet, issuer+"/userinfo", nil)
	r.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uw := httptest.NewRecorder()
	p.ServeHTTP(uw, r)
	var info map[string]any
	if uw.Code != 200 || json.Unmarshal(uw.Body.Bytes(), &info) != nil || info["sub"] != "1026" || info["preferred_username"] != "alice" {
		t.Fatalf("userinfo %d %s", uw.Code, uw.Body)
	}
}

func TestCodesAreSingleUseAndShortLived(t *testing.T) {
	p, c := newProvider(t)
	redirect := origin + "/api/auth/sso_callback"
	form := func(code string) url.Values {
		return url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {"openlist"}, "client_secret": {"ol"}}
	}
	req, _ := p.Begin(origin, authQuery("openlist", redirect, ""))
	code := codeFrom(t, p.Complete(req, bob))
	if w := exchange(p, form(code)); w.Code != 200 {
		t.Fatalf("client_secret_post: %d %s", w.Code, w.Body)
	}
	if w := exchange(p, form(code)); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_grant") {
		t.Fatalf("replay: %d %s", w.Code, w.Body)
	}
	req, _ = p.Begin(origin, authQuery("openlist", redirect, ""))
	code = codeFrom(t, p.Complete(req, bob))
	c.t = c.t.Add(CodeTTL + time.Second)
	if w := exchange(p, form(code)); w.Code != 400 {
		t.Fatalf("expired: %d", w.Code)
	}
}

func TestAWrongVerifierBurnsTheCode(t *testing.T) {
	p, _ := newProvider(t)
	redirect := origin + "/accounts/oidc/dsm/login/callback/"
	req, _ := p.Begin(origin, authQuery("paperless", redirect, "right"))
	code := codeFrom(t, p.Complete(req, alice))
	for _, v := range []string{"wrong", "right"} {
		w := exchange(p, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {v}}, "paperless", "s3cret")
		if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_grant") {
			t.Fatalf("%s: %d %s", v, w.Code, w.Body)
		}
	}
}

func TestTokenEndpointAuthenticatesTheClient(t *testing.T) {
	p, _ := newProvider(t)
	redirect := origin + "/accounts/oidc/dsm/login/callback/"
	for name, auth := range map[string][]string{"wrong secret": {"paperless", "nope"}, "other client": {"openlist", "ol"}, "none": nil} {
		req, _ := p.Begin(origin, authQuery("paperless", redirect, "v"))
		code := codeFrom(t, p.Complete(req, alice))
		w := exchange(p, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {"v"}}, auth...)
		if w.Code == 200 {
			t.Errorf("%s: accepted", name)
		}
	}
	req, _ := p.Begin(origin, authQuery("paperless", redirect, "v"))
	code := codeFrom(t, p.Complete(req, alice))
	if w := exchange(p, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {origin + "/other"}, "code_verifier": {"v"}}, "paperless", "s3cret"); w.Code != 400 {
		t.Errorf("redirect mismatch: %d", w.Code)
	}
}

func TestRedirectURIsMustBeSameOriginAllowlistedOrExactNative(t *testing.T) {
	p, _ := newProvider(t)
	for _, bad := range []struct{ client, redirect string }{
		{"paperless", "https://evil.example/accounts/oidc/dsm/login/callback/"},
		{"paperless", "https://nas.example:18444/accounts/oidc/dsm/login/callback/"},
		{"paperless", "http://nas.example:18443/accounts/oidc/dsm/login/callback/"},
		{"paperless", origin + "/accounts/oidc/dsm/login/callback/x"},
		{"paperless", origin + "/accounts/oidc/dsm/login/callback/#f"},
		{"immich", "app.immich:///oauth-callback/x"},
		{"immich", "evil.app:///oauth-callback"},
		{"paperless", "app.immich:///oauth-callback"},
		{"nobody", origin + "/accounts/oidc/dsm/login/callback/"},
	} {
		if _, err := p.Begin(origin, authQuery(bad.client, bad.redirect, "v")); !errors.Is(err, ErrInvalidRedirect) {
			t.Errorf("%s %s: %v", bad.client, bad.redirect, err)
		}
	}
	if _, err := p.Begin(origin, authQuery("immich", "app.immich:///oauth-callback", "v")); err != nil {
		t.Errorf("native: %v", err)
	}
	if _, err := p.Begin(origin, authQuery("paperless", origin+"/accounts/oidc/dsm/login/callback/?next=1", "v")); err != nil {
		t.Errorf("query on allowlisted path: %v", err)
	}
}

func TestRedirectableRequestErrors(t *testing.T) {
	p, _ := newProvider(t)
	redirect := origin + "/accounts/oidc/dsm/login/callback/"
	for name, q := range map[string]url.Values{
		"missing PKCE": authQuery("paperless", redirect, ""),
		"plain PKCE":   authQuery("paperless", redirect, "", "code_challenge", "abc", "code_challenge_method", "plain"),
		"not openid":   authQuery("paperless", redirect, "v", "scope", "profile"),
		"implicit":     authQuery("paperless", redirect, "v", "response_type", "token"),
	} {
		req, err := p.Begin(origin, q)
		var ae *AuthorizeError
		if !errors.As(err, &ae) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		u, _ := url.Parse(p.Fail(req, ae.Code))
		if u.Query().Get("error") == "" || u.Query().Get("state") != "st" || u.Query().Get("code") != "" {
			t.Errorf("%s: %s", name, u)
		}
	}
}

func TestRoleClaimsFollowTheClientMapping(t *testing.T) {
	p, _ := newProvider(t)
	claims := p.claims("abs", bob)
	if g, _ := claims["groups"].([]string); len(g) != 1 || g[0] != "user" {
		t.Errorf("abs bob %v", claims)
	}
	claims = p.claims("immich", alice)
	if claims["immich_role"] != "admin" || claims["groups"] != nil {
		t.Errorf("immich alice %v", claims)
	}
	claims = p.claims("paperless", bob)
	if g, ok := claims["groups"].([]string); !ok || len(g) != 0 {
		t.Errorf("paperless bob %v", claims)
	}
	if claims := p.claims("paperless", Subject{UID: 200001, Name: `CORP\Carol`}); claims["email"] != "uid-200001@nas.invalid" {
		t.Errorf("directory email %v", claims["email"])
	}
	if claims := p.claims("paperless", Subject{UID: 1030, Name: "Dave.K"}); claims["email"] != "dave.k@nas.invalid" {
		t.Errorf("mixed case email %v", claims["email"])
	}
}

func TestNonceIsOmittedWhenTheClientSentNone(t *testing.T) {
	p, _ := newProvider(t)
	redirect := origin + "/auth/openid/callback"
	req, _ := p.Begin(origin, authQuery("abs", redirect, "v"))
	code := codeFrom(t, p.Complete(req, alice))
	w := exchange(p, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {"v"}}, "abs", "abs")
	var tok struct {
		IDToken string `json:"id_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &tok)
	if _, ok := claimsOf(t, tok.IDToken)["nonce"]; ok {
		t.Fatal("nonce claim without a nonce")
	}
}

func TestDiscoveryAndLoopbackAuthorize(t *testing.T) {
	p, _ := newProvider(t)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest(http.MethodGet, issuer+"/.well-known/openid-configuration", nil))
	var d map[string]any
	json.Unmarshal(w.Body.Bytes(), &d)
	if d["issuer"] != issuer || d["authorization_endpoint"] != issuer+"/authorize" || d["token_endpoint"] != issuer+"/token" || d["jwks_uri"] != issuer+"/jwks" || d["userinfo_endpoint"] != issuer+"/userinfo" {
		t.Fatalf("discovery %v", d)
	}
	if m, _ := d["code_challenge_methods_supported"].([]any); len(m) != 1 || m[0] != "S256" {
		t.Fatalf("pkce methods %v", d["code_challenge_methods_supported"])
	}
	w = httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest(http.MethodGet, issuer+"/authorize?client_id=paperless", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("loopback authorize %d", w.Code)
	}
	w = httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest(http.MethodGet, issuer+"/jwks", nil))
	if !strings.Contains(w.Body.String(), p.key.KeyID) {
		t.Fatalf("jwks %s", w.Body)
	}
}

func TestNewProviderRefusesANonLoopbackIssuer(t *testing.T) {
	key, _ := LoadOrCreateSigningKey(filepath.Join(t.TempDir(), "k.pem"))
	for _, iss := range []string{"http://nas.example:18090", "http://0.0.0.0:18090", "http://127.0.0.1:18090/x/"} {
		if _, err := NewProvider(iss, "nas.invalid", key, nil, time.Now); err == nil {
			t.Errorf("%s accepted", iss)
		}
	}
}

func TestRevokeUIDDropsThatAccountsCodesAndTokens(t *testing.T) {
	p, _ := newProvider(t)
	redirect := origin + "/api/auth/sso_callback"
	signIn := func(s Subject) string {
		req, _ := p.Begin(origin, authQuery("openlist", redirect, ""))
		return codeFrom(t, p.Complete(req, s))
	}
	tokenFor := func(code string) (int, string) {
		w := exchange(p, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}}, "openlist", "ol")
		var tok struct {
			AccessToken string `json:"access_token"`
		}
		json.Unmarshal(w.Body.Bytes(), &tok)
		return w.Code, tok.AccessToken
	}
	userinfo := func(token string) int {
		r := httptest.NewRequest(http.MethodGet, issuer+"/userinfo", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		return w.Code
	}
	_, bobToken := tokenFor(signIn(bob))
	bobCode, aliceCode := signIn(bob), signIn(alice)
	p.RevokeUID(bob.UID)
	if code, _ := tokenFor(bobCode); code == http.StatusOK {
		t.Fatal("revoked code exchanged")
	}
	if userinfo(bobToken) == http.StatusOK {
		t.Fatal("revoked token accepted")
	}
	if code, _ := tokenFor(aliceCode); code != http.StatusOK {
		t.Fatalf("other account's code: %d", code)
	}
}
