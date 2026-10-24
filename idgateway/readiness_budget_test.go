package idgateway

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestUnknownReadinessClosesEveryUpstreamEntry(t *testing.T) {
	for _, access := range []string{"public", "dsm-users"} {
		t.Run(access, func(t *testing.T) {
			up := newUpstream(t, nil)
			ready := false
			f, c := newFront(t, ModeNone, up, func(cfg *FrontConfig) { cfg.Access = access; cfg.Ready = func(context.Context) bool { return ready } })
			b := newBrowser(t, f)
			if access != "public" {
				b = signedBrowser(t, f, c)
			}
			for _, path := range []string{"/", "/dav/file", "/share/file"} {
				if got := b.do("GET", "https://nas.example:18443"+path); got.Code != http.StatusServiceUnavailable {
					t.Fatalf("unknown readiness admitted %s: %d", path, got.Code)
				}
			}
			if up.hits != 0 {
				t.Fatal("unknown readiness reached upstream")
			}
			ready = true
			if got := b.do("GET", "https://nas.example:18443/"); got.Code != http.StatusOK {
				t.Fatalf("recovered upstream unavailable: %d", got.Code)
			}
		})
	}
}

// A slow App is starting, not broken: browsers get a localized page that
// refreshes itself, and API clients get a stable retryable code.
func TestUnreadyUpstreamShowsStartingPageInsteadOfBareError(t *testing.T) {
	up := newUpstream(t, nil)
	f, _ := newFront(t, ModeNone, up, func(cfg *FrontConfig) {
		cfg.Access = "public"
		cfg.Ready = func(context.Context) bool { return false }
	})
	b := newBrowser(t, f)
	page := b.do("GET", "https://nas.example:18443/web/", append(navigate, "Accept-Language", "zh-TW")...)
	body := page.Body.String()
	if page.Code != http.StatusServiceUnavailable || page.Header().Get("Retry-After") == "" || page.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("starting page status/headers: %d %v", page.Code, page.Header())
	}
	if !strings.Contains(body, `data-i18n="gateway.starting"`) || !strings.Contains(body, "啟動中") || !strings.Contains(body, `http-equiv="refresh"`) {
		t.Fatalf("starting page body: %s", body)
	}
	api := b.do("GET", "https://nas.example:18443/api/items")
	if api.Code != http.StatusServiceUnavailable || !strings.Contains(api.Body.String(), `"error":"app_starting"`) || api.Header().Get("Retry-After") == "" {
		t.Fatalf("starting API response: %d %s", api.Code, api.Body.String())
	}
	if up.hits != 0 {
		t.Fatal("unready upstream reached")
	}
}

func TestRefusedUpstreamConnectionShowsStartingPage(t *testing.T) {
	up := newUpstream(t, nil)
	f, _ := newFront(t, ModeNone, up, func(cfg *FrontConfig) { cfg.Access = "public" })
	up.Close()
	page := newBrowser(t, f).do("GET", "https://nas.example:18443/", navigate...)
	if page.Code != http.StatusServiceUnavailable || !strings.Contains(page.Body.String(), `data-i18n="gateway.starting"`) {
		t.Fatalf("refused upstream: %d %s", page.Code, page.Body.String())
	}
}
