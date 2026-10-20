package idgateway

import (
	"github.com/synopackageland/syno-pkg-land-sdk/internal/containerpreflight"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContainerEntryPreflight(t *testing.T) {
	missing := &containerpreflight.Failure{Code: "COMPOSE_CONTAINER_MANAGER_REQUIRED", MessageKey: "compose.containerManager.required", Message: "Install Container Manager from Package Center."}
	for _, tc := range []struct {
		name      string
		container bool
		err       error
		status    int
	}{
		{"missing", true, missing, 503}, {"ready", true, nil, 200}, {"nativeUnaffected", false, missing, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := entryHandler(Config{ContainerApp: tc.container, Listen: Listen{HTTP: 28000}}, func() error { calls++; return tc.err })
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "https://nas.example/open.cgi", nil))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.container && calls != 1 || !tc.container && calls != 0 {
				t.Fatal(calls)
			}
			if tc.status == 503 && (!strings.Contains(w.Body.String(), missing.MessageKey) || !strings.Contains(w.Body.String(), "Package Center") || w.Header().Get("Location") != "") {
				t.Fatal(w.Body.String())
			}
			if tc.status == http.StatusFound && w.Header().Get("Location") != "http://nas.example:28000/" {
				t.Fatal(w.Header())
			}
		})
	}
}
