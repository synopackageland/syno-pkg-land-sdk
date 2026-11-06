package nativehttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type sessionAuth struct {
	identity api.Identity
	err      error
	calls    int
}

func (a *sessionAuth) Authenticate(*http.Request) (api.Identity, error) {
	a.calls++
	return a.identity, a.err
}

type entryAuth struct {
	*sessionAuth
	mode string
}

func (a entryAuth) EntryMode() string { return a.mode }
func installed(t *testing.T, entry string) (string, string, []byte) {
	t.Helper()
	c := buildConfig()
	c.Gateway.Entry = entry
	var r Registry
	json.Unmarshal(fixture(t, "valid/header-page-files.json"), &r)
	if entry == "admin-only" {
		for name, o := range r.Operations {
			o.Permission = "administrators"
			r.Operations[name] = o
		}
	}
	b, _ := json.Marshal(r)
	a, e := Export(b, c)
	if e != nil {
		t.Fatal(e)
	}
	db, ib, e := a.Encode()
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	dp, ip := filepath.Join(dir, "synopkgland-api.json"), filepath.Join(dir, "synopkgland-http-inventory.json")
	if e = os.WriteFile(dp, db, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(ip, ib, 0600); e != nil {
		t.Fatal(e)
	}
	return dp, ip, db
}
func describeRequest(body string) *http.Request {
	r := httptest.NewRequest("POST", "https://dsm.example/cgi/api.cgi", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://dsm.example")
	r.Header.Set("Cookie", "id=fixture-session")
	r.Header.Set("X-SYNO-TOKEN", "fixture-dsm-token")
	return r
}
func TestMetadataDescribeSessionAndEntry(t *testing.T) {
	for _, tc := range []struct {
		name, entry, gate string
		admin             bool
		authErr           error
		want              string
	}{
		{name: "ordinary", want: ""},
		{name: "expired DSM session", authErr: api.ErrNoSession, want: "session_required"},
		{name: "admin-only ordinary denied", entry: "admin-only", want: "admin_required"},
		{name: "admin-only admin", entry: "admin-only", admin: true},
		{name: "app privilege granted", entry: "app-privilege", gate: "app-privilege"},
		{name: "app privilege denied", entry: "app-privilege", gate: "app-privilege", authErr: api.ErrAppPrivilegeDenied, want: "app_privilege_denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dp, ip, db := installed(t, tc.entry)
			a := &sessionAuth{identity: api.Identity{User: "fixture-user", Admin: tc.admin}, err: tc.authErr}
			var auth api.Authenticator = a
			if tc.gate != "" {
				auth = entryAuth{a, tc.gate}
			}
			h, e := NewDescriptionHandler(auth, dp, ip)
			if e != nil {
				t.Fatal(e)
			}
			// Snapshot remains immutable if a package file changes after validation.
			os.WriteFile(dp, []byte(`{"secret":"must not be served"}`), 0600)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, describeRequest(`{"method":"synopkgland.describe","params":{}}`))
			var out struct {
				Success bool
				Data    json.RawMessage
				Error   struct{ Code string }
			}
			if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
				t.Fatal(e)
			}
			if out.Error.Code != tc.want || out.Success != (tc.want == "") {
				t.Fatalf("unexpected response: %s", w.Body.String())
			}
			if tc.want == "" && !same(json.RawMessage(db), out.Data) {
				t.Fatal("describe changed packaged metadata")
			}
			if a.calls != 1 {
				t.Fatal("DSM authentication not called exactly once")
			}
		})
	}
}
func TestMetadataDescribeRefusesCredentialAndDispatchInputs(t *testing.T) {
	dp, ip, _ := installed(t, "")
	a := &sessionAuth{identity: api.Identity{User: "fixture-user"}}
	h, e := NewDescriptionHandler(a, dp, ip)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name, body string
		change     func(*http.Request)
		want       string
	}{
		{name: "no DSM cookie", change: func(r *http.Request) { r.Header.Del("Cookie") }, want: "session_required"},
		{name: "no DSM token", change: func(r *http.Request) { r.Header.Del("X-SYNO-TOKEN") }, want: "credential_not_ready"},
		{name: "native header is insufficient", change: func(r *http.Request) {
			r.Header.Del("Cookie")
			r.Header.Del("X-SYNO-TOKEN")
			r.Header.Set("Authorization", "Bearer must-not-echo")
		}, want: "session_required"},
		{name: "conflicting DSM token", change: func(r *http.Request) { r.Header.Set("SynoToken", "conflicting") }, want: "session_required"},
		{name: "cross origin", change: func(r *http.Request) { r.Header.Set("Origin", "https://other.example") }, want: "cross_origin"},
		{name: "native params", body: `{"method":"synopkgland.describe","params":{"token":"must-not-echo"}}`, want: "bad_request"},
		{name: "missing params", body: `{"method":"synopkgland.describe"}`, want: "bad_request"},
		{name: "null params", body: `{"method":"synopkgland.describe","params":null}`, want: "bad_request"},
		{name: "unknown method", body: `{"method":"documents.list","params":{}}`, want: "unknown_method"},
		{name: "batch", body: `{"v":1,"calls":[{"method":"synopkgland.describe","params":{}}]}`, want: "unknown_method"},
		{name: "query dispatch", change: func(r *http.Request) { r.URL.RawQuery = "method=synopkgland.describe" }, want: "unknown_method"},
		{name: "GET", change: func(r *http.Request) { r.Method = "GET" }, want: "unknown_method"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			if body == "" {
				body = `{"method":"synopkgland.describe","params":{}}`
			}
			r := describeRequest(body)
			if tc.change != nil {
				tc.change(r)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var out struct {
				Success bool
				Data    json.RawMessage
				Error   struct{ Code string }
			}
			json.Unmarshal(w.Body.Bytes(), &out)
			if out.Success || out.Error.Code != tc.want {
				t.Fatalf("unexpected response: %s", w.Body.String())
			}
			if len(out.Data) > 0 || bytes.Contains(w.Body.Bytes(), []byte("must-not-echo")) {
				t.Fatal("private input or metadata disclosed")
			}
		})
	}
}
func TestMetadataDescribeRejectsMissingGateAndArtifactDrift(t *testing.T) {
	dp, ip, _ := installed(t, "app-privilege")
	a := &sessionAuth{identity: api.Identity{User: "fixture-user"}}
	if _, e := NewDescriptionHandler(a, dp, ip); e == nil {
		t.Fatal("app privilege gate missing")
	}
	if _, e := NewDescriptionHandler(entryAuth{a, "admin-only"}, dp, ip); e == nil {
		t.Fatal("mismatched entry gate accepted")
	}
	if _, e := NewDescriptionHandler(nil, dp, ip); e == nil {
		t.Fatal("nil DSM auth accepted")
	}
	os.WriteFile(ip, []byte(`{"version":1}`), 0600)
	if _, e := NewDescriptionHandler(entryAuth{a, "app-privilege"}, dp, ip); e == nil {
		t.Fatal("invalid packaged inventory accepted")
	}
}
