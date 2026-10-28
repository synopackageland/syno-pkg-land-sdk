package idgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestAppPrivilegeEntryRechecksRequestsAndUpgrades(t *testing.T) {
	allowed := true
	membership := &changingMembership{groups: []string{"users"}}
	up := newUpstream(t, nil)
	f, c := newFront(t, ModeNone, up, func(cfg *FrontConfig) {
		cfg.Entry = "app-privilege"
		cfg.Passthrough = nil
		cfg.Membership = membership
		cfg.appPrivilege = func(_ context.Context, user, app, ip string) (bool, error) {
			if user != "alice" || app != "SYNO.SDS.App.Instance" || ip != "192.0.2.7" {
				t.Fatalf("wrong privilege check: %s %s %s", user, app, ip)
			}
			return allowed, nil
		}
	})
	b := newBrowser(t, f)
	b.cookies[f.cookie] = "ordinary-session"
	f.sessions["ordinary-session"] = session{subject: Subject{UID: 1026, Name: "alice"}, ip: "192.0.2.7", expires: c.now().Add(time.Minute)}
	if w := b.do("GET", "https://app.example/api"); w.Code != http.StatusOK {
		t.Fatalf("admitted user: %d %s", w.Code, w.Body)
	}
	if !f.upgradeAllowed(context.Background(), "ordinary-session", "192.0.2.7") {
		t.Fatal("admitted upgrade refused")
	}
	allowed = false
	if w := b.do("GET", "https://app.example/api"); w.Code != http.StatusForbidden {
		t.Fatalf("revoked user: %d %s", w.Code, w.Body)
	}
	if f.upgradeAllowed(context.Background(), "ordinary-session", "192.0.2.7") {
		t.Fatal("revoked upgrade remains admitted")
	}
}

func TestEntryFloorClosesEveryPublicProxyShortcut(t *testing.T) {
	for _, mode := range []Mode{ModeNone, ModeHeader, ModeToken, ModeOIDC} {
		t.Run(string(mode), func(t *testing.T) {
			up := newUpstream(t, nil)
			membership := &changingMembership{groups: []string{"users"}}
			f, c := newFront(t, mode, up, func(cfg *FrontConfig) {
				cfg.Entry = "admin-only"
				cfg.Access = ""
				cfg.Passthrough = nil
				cfg.Membership = membership
				cfg.Tokens = &staticTokens{}
			})
			b := newBrowser(t, f)
			for _, path := range []string{"/", "/api", "/dav/file", "/share/file", "/socket"} {
				w := b.do("GET", "https://app.example"+path, "Accept", "application/json")
				if w.Code == http.StatusOK {
					t.Fatalf("anonymous reached %s", path)
				}
			}
			b.cookies[f.cookie] = "ordinary-session"
			f.sessions["ordinary-session"] = session{subject: Subject{UID: 1026, Name: "alice"}, ip: "192.0.2.7", expires: c.now().Add(time.Minute)}
			for _, path := range []string{"/api", "/dav/file", "/share/file"} {
				w := b.do("GET", "https://app.example"+path)
				if w.Code != http.StatusForbidden {
					t.Fatalf("non-admin reached %s: %d %s", path, w.Code, w.Body)
				}
			}
			up.mu.Lock()
			hits := up.hits
			up.mu.Unlock()
			if hits != 0 {
				t.Fatalf("upstream received %d denied requests", hits)
			}
			membership.groups = []string{"administrators"}
			w := b.do("GET", "https://app.example/api")
			if w.Code != http.StatusOK {
				t.Fatalf("administrator denied: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestAdminEntryRechecksOIDCCodesAndAccessTokens(t *testing.T) {
	membership := &changingMembership{groups: []string{"administrators"}}
	f, _ := newFront(t, ModeOIDC, newUpstream(t, nil), func(cfg *FrontConfig) {
		cfg.Entry = "admin-only"
		cfg.Passthrough = nil
		cfg.Membership = membership
	})
	p := f.cfg.Provider
	redirect := origin + "/accounts/oidc/dsm/login/callback/"
	exchangeFor := func() *httptest.ResponseRecorder {
		req, err := p.Begin(origin, authQuery("paperless", redirect, "verifier"))
		if err != nil {
			t.Fatal(err)
		}
		code := codeFrom(t, p.Complete(req, alice))
		return exchange(p, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {"verifier"}}, "paperless", "s3cret")
	}
	w := exchangeFor()
	if w.Code != http.StatusOK {
		t.Fatalf("admin token: %d %s", w.Code, w.Body)
	}
	var token struct {
		Access string `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil || token.Access == "" {
		t.Fatal("missing access token")
	}
	membership.groups = []string{"users"}
	if denied := exchangeFor(); denied.Code == http.StatusOK {
		t.Fatal("demoted administrator redeemed an OIDC code")
	}
	r := httptest.NewRequest("GET", issuer+"/userinfo", nil)
	r.Header.Set("Authorization", "Bearer "+token.Access)
	w = httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("demoted administrator used access token: %d %s", w.Code, w.Body)
	}
}

func TestAppPrivilegeOIDCRetainsBrowserAddressAndRechecksTokens(t *testing.T) {
	allowed := true
	f, _ := newFront(t, ModeOIDC, newUpstream(t, nil), func(cfg *FrontConfig) {
		cfg.Entry = "app-privilege"
		cfg.Passthrough = nil
		cfg.Membership = &changingMembership{groups: []string{"users"}}
		cfg.appPrivilege = func(_ context.Context, user, app, ip string) (bool, error) {
			if ip != "192.0.2.7" {
				t.Fatalf("token endpoint used its peer instead of browser address: %s", ip)
			}
			return allowed, nil
		}
	})
	p := f.cfg.Provider
	redirect := origin + "/accounts/oidc/dsm/login/callback/"
	exchangeFor := func() *httptest.ResponseRecorder {
		req, err := p.Begin(origin, authQuery("paperless", redirect, "verifier"))
		if err != nil {
			t.Fatal(err)
		}
		req.clientIP = "192.0.2.7"
		code := codeFrom(t, p.Complete(req, alice))
		return exchange(p, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {"verifier"}}, "paperless", "s3cret")
	}
	w := exchangeFor()
	if w.Code != http.StatusOK {
		t.Fatalf("admitted user: %d %s", w.Code, w.Body)
	}
	var token struct {
		Access string `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil || token.Access == "" {
		t.Fatal("missing access token")
	}
	allowed = false
	if denied := exchangeFor(); denied.Code == http.StatusOK {
		t.Fatal("revoked user redeemed an OIDC code")
	}
	r := httptest.NewRequest("GET", issuer+"/userinfo", nil)
	r.Header.Set("Authorization", "Bearer "+token.Access)
	w = httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked user used access token: %d %s", w.Code, w.Body)
	}
}

func TestEntryCannotBeCombinedWithPublicGatewayExceptions(t *testing.T) {
	for _, entry := range []string{"admin-only", "app-privilege"} {
		for _, tc := range []struct {
			access, upstream    string
			permanent, optional int
		}{
			{access: "public"}, {upstream: "public"}, {permanent: 1}, {optional: 1},
		} {
			if err := validateEntry(entry, tc.access, tc.upstream, tc.permanent, tc.optional); err == nil {
				t.Fatalf("accepted %s %+v", entry, tc)
			}
			if err := validateEntry("", tc.access, tc.upstream, tc.permanent, tc.optional); err != nil {
				t.Fatalf("legacy no-entry exception changed: %v", err)
			}
		}
	}
}
