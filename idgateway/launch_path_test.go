package idgateway

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFixedLaunchPathKeepsOriginAndNativeFragment(t *testing.T) {
	cfg, err := providerOnlyConfig(t, map[string]any{"launch_path": "/#/sso?identifier=fixed-organization"})
	if err != nil {
		t.Fatal(err)
	}
	cfg.NativeIngress = true
	w := httptest.NewRecorder()
	entryHandler(cfg, func() error { return nil }).ServeHTTP(w, httptest.NewRequest("GET", "https://nas.example:5001/webman/3rdparty/App/sso/open.cgi?launch_path=//foreign.example", nil))
	if got := w.Header().Get("Location"); got != "https://nas.example:18443/#/sso?identifier=fixed-organization" {
		t.Fatalf("fixed launch: %d %q", w.Code, got)
	}
}

func TestFixedLaunchPathRejectsExternalAndReservedTargets(t *testing.T) {
	for _, path := range []string{"https://foreign.example/", "//foreign.example/", "/\\foreign", "/../escape", "/%2e%2e/escape", "/_sso/authorize", "/_sso", "/%2f/foreign", "/bad\r\nX: value", "/bad%0aheader", strings.Repeat("/a", 1100)} {
		if _, err := providerOnlyConfig(t, map[string]any{"launch_path": path}); err == nil {
			t.Fatalf("invalid launch path accepted: %q", path)
		}
	}
}
