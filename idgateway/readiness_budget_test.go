package idgateway

import (
	"context"
	"net/http"
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
