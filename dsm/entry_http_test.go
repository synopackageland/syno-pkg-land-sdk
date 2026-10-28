package dsm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func TestAdminEntryRejectsPublicDispatchBeforeAnyAppCode(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		auth       fixedAuth
	}{
		{"anonymous", "session_required", fixedAuth{err: api.ErrNoSession}},
		{"ordinary", "admin_required", fixedAuth{id: api.Identity{User: "ordinary"}}},
		{"administrator", "", fixedAuth{id: api.Identity{User: "operator", Admin: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate, err := NewEntryGate(tc.auth, EntryAdminOnly, "DemoApp", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			executed := 0
			h := func(_ context.Context, c api.Caller, _ json.RawMessage) (any, error) {
				executed++
				if !c.Admin() {
					t.Error("public App handler received a caller below the entry floor")
				}
				return "ok", nil
			}
			srv, err := api.NewServerWithPermissions(gate, []byte(`{"schema":1,"default":"administrators","methods":{"app.public":"public","app.stream":"public","app.ndjson":"public"}}`),
				api.Handle("app.public", h),
				api.HandleStream("app.stream", func(ctx context.Context, c api.Caller, w http.ResponseWriter, r *http.Request) error {
					data, _ := h(ctx, c, nil)
					api.WriteData(w, data)
					return nil
				}),
				api.HandleResponse("app.ndjson", func(ctx context.Context, c api.Caller, w http.ResponseWriter, r *http.Request, p json.RawMessage) error {
					_, _ = h(ctx, c, p)
					w.Header().Set("Content-Type", "application/x-ndjson")
					_, err := w.Write([]byte("{\"done\":true}\n"))
					return err
				}))
			if err != nil {
				t.Fatal(err)
			}
			for _, body := range []string{`{"method":"app.public"}`, `{"method":"app.ndjson"}`, `{"v":1,"calls":[{"method":"app.public"},{"method":"app.public"}]}`} {
				r := httptest.NewRequest("POST", "https://app.example/api.cgi", strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", "https://app.example")
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, r)
				if tc.code != "" && !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
					t.Errorf("%s: %s", body, w.Body.String())
				}
			}
			for _, verb := range []string{"GET", "POST"} {
				r := httptest.NewRequest(verb, "https://app.example/api.cgi?method=app.stream", strings.NewReader("SynoToken=fixture"))
				r.Header.Set("Origin", "https://app.example")
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, r)
				if tc.code != "" && !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
					t.Errorf("%s transfer: %s", verb, w.Body.String())
				}
			}
			if tc.code != "" && executed != 0 || tc.code == "" && executed != 6 {
				t.Fatalf("executed=%d", executed)
			}
		})
	}
}
