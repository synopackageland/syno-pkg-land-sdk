package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type descriptionAuth struct {
	id    Identity
	err   error
	calls int
}

func (a *descriptionAuth) Authenticate(*http.Request) (Identity, error) {
	a.calls++
	return a.id, a.err
}
func TestDescriptionRequiresDSMCredentialsAndPreservesOtherRequests(t *testing.T) {
	file := filepath.Join(t.TempDir(), "synopkgland-api.json")
	if err := os.WriteFile(file, []byte(`{"format":"synopkgland.api","version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := &descriptionAuth{id: Identity{User: "fixtureuser1"}}
	nextCalls := 0
	h := WithDescription(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nextCalls++; w.WriteHeader(204) }), a, file)
	for _, tc := range []struct{ name, cookie, token, want string }{{"anonymous", "", "", "session_required"}, {"no token", "id=sid", "", "credential_not_ready"}, {"authenticated", "id=sid", "token", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://nas/cgi/api.cgi", bytes.NewBufferString(`{"method":"synopkgland.describe","params":{}}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "http://nas")
			if tc.cookie != "" {
				r.Header.Set("Cookie", tc.cookie)
			}
			if tc.token != "" {
				r.Header.Set("X-SYNO-TOKEN", tc.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var out struct {
				Success bool
				Error   struct{ Code string }
				Data    json.RawMessage
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Error.Code != tc.want || out.Success != (tc.want == "") {
				t.Fatalf("unexpected response %s", w.Body.String())
			}
		})
	}
	if a.calls != 1 || nextCalls != 0 {
		t.Fatalf("auth=%d next=%d", a.calls, nextCalls)
	}
	r := httptest.NewRequest("POST", "http://nas/cgi/api.cgi", bytes.NewBufferString(`{"method":"notes.list","params":{}}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || nextCalls != 1 {
		t.Fatal("business request not preserved")
	}
}
func TestDescriptionEntryDenialAndInvalidParams(t *testing.T) {
	a := &descriptionAuth{err: ErrAdminRequired}
	h := WithDescription(http.NotFoundHandler(), a, filepath.Join(t.TempDir(), "missing"))
	r := httptest.NewRequest("POST", "http://nas/cgi/api.cgi", bytes.NewBufferString(`{"method":"synopkgland.describe","params":{}}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://nas")
	r.Header.Set("Cookie", "id=sid")
	r.Header.Set("X-SYNO-TOKEN", "token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !bytes.Contains(w.Body.Bytes(), []byte(`"admin_required"`)) {
		t.Fatalf("%s", w.Body.String())
	}
}
func TestDescribeRoutesIncludesOnlyActualBindingsAndEffectivePolicies(t *testing.T) {
	raw := []byte(`{"schema":1,"default":"administrators","methods":{"notes.list":"authenticated"}}`)
	routes := []Route{Handle("notes.list", func(_ context.Context, _ Caller, _ json.RawMessage) (any, error) {
		t.Fatal("export ran handler")
		return nil, nil
	}), Handle("notes.delete", func(_ context.Context, _ Caller, _ json.RawMessage) (any, error) { return nil, nil }), HandleStream("notes.upload", func(context.Context, Caller, http.ResponseWriter, *http.Request) error { return nil })}
	out, err := DescribeRoutes(raw, routes...)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0]["method"] != "notes.delete" || out[0]["permission"] != "administrators" || out[1]["permission"] != "authenticated" || out[2]["kind"] != "transfer" {
		t.Fatalf("bad registry export: %v", out)
	}
}
