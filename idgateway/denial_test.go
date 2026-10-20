package idgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func finishCGI(t *testing.T, b *browser, start *httptest.ResponseRecorder, verifier SessionVerifier) *httptest.ResponseRecorder {
	t.Helper()
	loc, err := url.Parse(start.Header().Get("Location"))
	if err != nil || start.Code != http.StatusFound {
		t.Fatalf("start: %d %s", start.Code, start.Body)
	}
	h := newCGI(&fakeDSM{})
	h.Verifier = verifier
	w := cgiPost(t, h, "?"+loc.RawQuery, "tok")
	if w.Code != http.StatusFound {
		t.Fatalf("CGI: %d %s", w.Code, w.Body)
	}
	return b.do("GET", w.Header().Get("Location"), navigate...)
}

func assertDenial(t *testing.T, w *httptest.ResponseRecorder, status int, code, key, message string, retry bool) {
	t.Helper()
	body := w.Body.String()
	if w.Code != status || !strings.Contains(body, `data-error-code="`+code+`"`) || !strings.Contains(body, `data-i18n="gateway.`+key+`"`) || !strings.Contains(body, message) {
		t.Fatalf("denial: %d %s", w.Code, body)
	}
	if strings.Contains(body, `data-i18n="gateway.retry"`) != retry {
		t.Fatalf("retry=%v: %s", retry, body)
	}
	if w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("headers: %v", w.Header())
	}
}

func TestGatewayInitialAdmissionDenial(t *testing.T) {
	for _, mode := range []Mode{ModeNone, ModeHeader, ModeToken, ModeOIDC} {
		for _, access := range []string{"dsm-admins", "dsm-groups"} {
			t.Run(string(mode)+"/"+access, func(t *testing.T) {
				up := newUpstream(t, nil)
				membership := &changingMembership{groups: []string{"users"}}
				tokens := &staticTokens{}
				f, _ := newFront(t, mode, up, func(c *FrontConfig) {
					c.Access = access
					c.Groups = []string{"admitted"}
					c.Membership = membership
					c.Tokens = tokens
				})
				b := newBrowser(t, f)
				target := "https://nas.example:18443/"
				if mode == ModeOIDC {
					target += "_sso/authorize?" + authQuery("paperless", "https://nas.example:18443/accounts/oidc/dsm/login/callback/", "v").Encode()
				}
				verifier := AccessVerifier{Next: verifierFunc(func(*http.Request) (api.Identity, error) { return api.Identity{User: "alice"}, nil }), Access: access, Groups: []string{"admitted"}, Membership: membership}
				w := finishCGI(t, b, b.do("GET", target, navigate...), verifier)
				code, key, message := "dsm_admin_required", "admin_required", "This App requires a DSM administrator."
				if access == "dsm-groups" {
					code, key, message = "dsm_group_required", "group_required", "Your DSM account is not in a group allowed to use this App."
				}
				assertDenial(t, w, 403, code, key, message, false)
				if strings.Contains(w.Body.String(), "Try again") || up.hits != 0 || tokens.calls != 0 || len(f.sessions) != 0 {
					t.Fatalf("denial created access or retry: %s", w.Body)
				}
			})
		}
	}
}

func TestGatewaySessionAndVerificationFailuresAreDistinct(t *testing.T) {
	for _, tc := range []struct {
		name               string
		err                error
		status             int
		code, key, message string
	}{
		{"expired", fmt.Errorf("wrapped: %w", api.ErrNoSession), 401, "dsm_sign_in_required", "sign_in_required", "Sign in to DSM on this address"},
		{"denied", fmt.Errorf("wrapped: %w", api.ErrAdminRequired), 403, "dsm_admin_required", "admin_required", "requires a DSM administrator"},
		{"unavailable", errors.New("private backend detail"), 503, "dsm_verification_failed", "verification_failed", "DSM could not verify"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := newFront(t, ModeHeader, newUpstream(t, nil), func(c *FrontConfig) { c.Access = "dsm-admins" })
			b := newBrowser(t, f)
			w := finishCGI(t, b, b.do("GET", "https://nas.example:18443/library", navigate...), verifierFunc(func(*http.Request) (api.Identity, error) { return api.Identity{}, tc.err }))
			assertDenial(t, w, tc.status, tc.code, tc.key, tc.message, tc.status != 403)
			if strings.Contains(w.Body.String(), "private backend detail") {
				t.Fatal("private error leaked")
			}
			if tc.status == 401 && !strings.Contains(w.Body.String(), `href="https://nas.example:5001/"`) {
				t.Fatal("missing DSM sign-in link")
			}
		})
	}
}

func TestGatewayDemotionDeniesNextNavigationAndAPI(t *testing.T) {
	for _, access := range []string{"dsm-admins", "dsm-groups"} {
		t.Run(access, func(t *testing.T) {
			up := newUpstream(t, nil)
			membership := &changingMembership{groups: []string{"administrators", "admitted"}}
			tokens := &staticTokens{}
			f, c := newFront(t, ModeToken, up, func(c *FrontConfig) {
				c.Access = access
				c.Groups = []string{"admitted"}
				c.Membership = membership
				c.Tokens = tokens
			})
			b := signedBrowser(t, f, c)
			if w := b.do("GET", "https://nas.example:18443/api"); w.Code != 200 {
				t.Fatal(w.Code)
			}
			membership.groups = []string{"users"}
			code, key, message := "dsm_admin_required", "admin_required", "requires a DSM administrator"
			if access == "dsm-groups" {
				code, key, message = "dsm_group_required", "group_required", "not in a group allowed"
			}
			assertDenial(t, b.do("GET", "https://nas.example:18443/", navigate...), 403, code, key, message, false)
			w := b.do("GET", "https://nas.example:18443/api", "Accept", "application/json")
			var got map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("API: %s", w.Body)
			}
			if w.Code != 403 || got["error"] != code || got["message_key"] != "gateway."+key || !strings.Contains(got["message"], message) {
				t.Fatalf("API: %d %v", w.Code, got)
			}
			if up.hits != 1 || tokens.calls != 1 {
				t.Fatal("denied request reached App", up.hits, tokens.calls)
			}
		})
	}
}

type brokenMembership struct{}

func (brokenMembership) Groups(context.Context, string) ([]string, error) {
	return nil, errors.New("private NSS failure")
}

func TestGatewayMembershipFailureIsNotPermissionDenial(t *testing.T) {
	f, c := newFront(t, ModeHeader, newUpstream(t, nil), func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = brokenMembership{} })
	b := newBrowser(t, f)
	w := b.do("GET", dsmBounce(t, b.do("GET", "https://nas.example:18443/", navigate...), alice, c.t), navigate...)
	assertDenial(t, w, 503, "dsm_verification_failed", "verification_failed", "DSM could not verify", true)
	if len(f.sessions) != 0 {
		t.Fatal("failed admission created a session")
	}
	v := AccessVerifier{Next: verifierFunc(func(*http.Request) (api.Identity, error) { return api.Identity{User: "alice"}, nil }), Access: "dsm-admins", Membership: brokenMembership{}}
	if _, err := v.Authenticate(httptest.NewRequest("GET", "/", nil)); err == nil || errors.Is(err, api.ErrAdminRequired) {
		t.Fatal("NSS failure confused with denial", err)
	}
}

func TestGatewaySilentOIDCDenialUsesProtocolError(t *testing.T) {
	f, _ := newFront(t, ModeOIDC, newUpstream(t, nil), func(c *FrontConfig) { c.Access = "dsm-admins" })
	b := newBrowser(t, f)
	target := "https://nas.example:18443/_sso/authorize?" + authQuery("paperless", "https://nas.example:18443/accounts/oidc/dsm/login/callback/", "v", "prompt", "none").Encode()
	w := finishCGI(t, b, b.do("GET", target, navigate...), verifierFunc(func(*http.Request) (api.Identity, error) { return api.Identity{}, api.ErrAdminRequired }))
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 302 || loc.Query().Get("error") != "access_denied" || loc.Query().Get("state") == "" {
		t.Fatalf("silent denial: %d %s", w.Code, loc)
	}
}

func TestGatewayDenialLocalizesAndEscapesUntrustedErrors(t *testing.T) {
	for _, tc := range []struct{ lang, want string }{{"zh-TW", "此 App 需要 DSM 管理員權限。"}, {"fr-FR", "Cette App nécessite un administrateur DSM."}, {"xx-YY", "This App requires a DSM administrator."}} {
		f, _ := newFront(t, ModeHeader, newUpstream(t, nil), func(c *FrontConfig) { c.Access = "dsm-admins" })
		b := newBrowser(t, f)
		loc, _ := url.Parse(b.do("GET", "https://nas.example:18443/", navigate...).Header().Get("Location"))
		target := loc.Query().Get("return") + "?" + url.Values{"txn": {loc.Query().Get("txn")}, "error": {"access_denied"}}.Encode()
		w := b.do("GET", target, "Accept-Language", tc.lang)
		if w.Code != 403 || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("locale %s: %d %s", tc.lang, w.Code, w.Body)
		}
	}
	f, _ := newFront(t, ModeHeader, newUpstream(t, nil), nil)
	b := newBrowser(t, f)
	loc, _ := url.Parse(b.do("GET", "https://nas.example:18443/", navigate...).Header().Get("Location"))
	target := loc.Query().Get("return") + "?" + url.Values{"txn": {loc.Query().Get("txn")}, "error": {"<script>alert(1)</script>"}}.Encode()
	w := b.do("GET", target, navigate...)
	assertDenial(t, w, 503, "dsm_verification_failed", "verification_failed", "DSM could not verify", true)
	if strings.Contains(w.Body.String(), "<script>") {
		t.Fatal("untrusted error rendered")
	}
}

func TestGatewayAssertionRedemptionRechecksAdmission(t *testing.T) {
	for _, mode := range []Mode{ModeHeader, ModeOIDC} {
		t.Run(string(mode), func(t *testing.T) {
			membership := &changingMembership{groups: []string{"administrators"}}
			up := newUpstream(t, nil)
			f, c := newFront(t, mode, up, func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = membership })
			b := newBrowser(t, f)
			target := "https://nas.example:18443/"
			if mode == ModeOIDC {
				target += "_sso/authorize?" + authQuery("paperless", "https://nas.example:18443/accounts/oidc/dsm/login/callback/", "v").Encode()
			}
			ret := dsmBounce(t, b.do("GET", target, navigate...), alice, c.t)
			membership.groups = nil
			assertDenial(t, b.do("GET", ret, navigate...), 403, "dsm_admin_required", "admin_required", "requires a DSM administrator", false)
			if up.hits != 0 || len(f.sessions) != 0 {
				t.Fatal("revoked assertion granted access")
			}
		})
	}
}

func TestGatewayCatalogsCoverSDKLocales(t *testing.T) {
	files, err := denialCatalogs.ReadDir("i18n")
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := os.ReadDir("../ts/i18n/identity")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(sdk) || len(files) != len(denialLocales) {
		t.Fatal("gateway and SDK locales differ")
	}
	for _, file := range sdk {
		raw, err := denialCatalogs.ReadFile("i18n/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		var messages map[string]string
		if err = json.Unmarshal(raw, &messages); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"title", "admin_required", "group_required", "sign_in_required", "verification_failed", "sign_in", "retry", "direct_title", "direct_notice", "direct_continue"} {
			if strings.TrimSpace(messages[key]) == "" {
				t.Errorf("%s: missing %s", file.Name(), key)
			}
		}
	}
	for _, tc := range []struct{ accept, want string }{
		{"", "en"}, {"en-US", "en"}, {"zh-Hant", "zh-TW"}, {"zh-Hans", "zh-CN"}, {"de;q=0.2,ja;q=0.9", "ja"}, {"invalid;language", "en"}, {"xx-YY", "en"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", tc.accept)
		got, _ := denialMessages(r)
		if got != tc.want {
			t.Errorf("%q: %s, want %s", tc.accept, got, tc.want)
		}
	}
}
