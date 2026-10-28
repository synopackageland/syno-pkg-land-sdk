package dsm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func TestAdmitPrincipalRefusesDisabledAndUnprivilegedAccounts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       string
		enabled    bool
		enabledErr error
		allowed    bool
		want       error
		lookups    int
	}{
		{"allowed", EntryAppPrivilege, true, nil, true, nil, 1},
		{"disabled or expired", EntryAppPrivilege, false, nil, true, api.ErrAccountDisabled, 0},
		{"account bridge unavailable", EntryAppPrivilege, false, errNativeAccount, true, errNativeAccount, 0},
		{"no application privilege", EntryAppPrivilege, true, nil, false, api.ErrAppPrivilegeDenied, 1},
		{"admin-only app", EntryAdminOnly, true, nil, true, api.ErrAdminRequired, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &lookup{allowed: tc.allowed}
			gate, _ := newTestGate(t, tc.mode, ordinary("unused"), l)
			gate.enabled = func(_ context.Context, user string) (bool, error) {
				if user != "alice" {
					t.Errorf("account check for %q", user)
				}
				return tc.enabled, tc.enabledErr
			}
			id, err := gate.AdmitPrincipal(context.Background(), "alice")
			if !errors.Is(err, tc.want) {
				t.Fatalf("AdmitPrincipal = %v, want %v", err, tc.want)
			}
			if l.calls != tc.lookups {
				t.Fatalf("Application Privileges lookups = %d", l.calls)
			}
			if tc.want == nil && (id.User != "alice" || id.Admin || l.ip != integrationAddr || l.app != "SYNO.SDS.DemoApp.Instance") {
				t.Fatalf("admitted %+v from %s for %s", id, l.ip, l.app)
			}
		})
	}
}

func TestAdmitPrincipalRefusesInvalidNamesBeforeNativeChecks(t *testing.T) {
	l := &lookup{allowed: true}
	gate, _ := newTestGate(t, EntryAppPrivilege, ordinary("unused"), l)
	gate.enabled = func(context.Context, string) (bool, error) { t.Fatal("account bridge ran"); return true, nil }
	for _, name := range []string{"", " alice", "a/b", "a\x00b"} {
		if _, err := gate.AdmitPrincipal(context.Background(), name); err == nil {
			t.Fatalf("admitted %q", name)
		}
	}
}

// End to end through api.Server: the integration handler's ActAs uses this gate.
func TestActAsThroughEntryGate(t *testing.T) {
	for _, tc := range []struct {
		name             string
		enabled, allowed bool
		want             error
	}{
		{"enabled and allowed", true, true, nil},
		{"disabled", false, true, api.ErrAccountDisabled},
		{"no application privilege", true, false, api.ErrAppPrivilegeDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &lookup{allowed: tc.allowed}
			gate, _ := newTestGate(t, EntryAppPrivilege, fixedAuth{err: api.ErrNoSession}, l)
			gate.enabled = func(context.Context, string) (bool, error) { return tc.enabled, nil }
			var caller api.Caller
			var actErr error
			srv, err := api.NewServerWithPermissions(gate, []byte(`{"schema":1,"default":"administrators","methods":{"app.callback":"integration"}}`),
				api.HandleIntegration("app.callback", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					caller, actErr = api.ActAs(r.Context(), r, "alice")
				})))
			if err != nil {
				t.Fatal(err)
			}
			srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api.cgi?method=app.callback", nil))
			if !errors.Is(actErr, tc.want) || (tc.want == nil) != caller.Authenticated() || caller.Admin() {
				t.Fatalf("ActAs = %+v, %v", caller, actErr)
			}
		})
	}
}

func TestParseNativeAccount(t *testing.T) {
	for _, tc := range []struct {
		out     string
		err     error
		enabled bool
		fails   bool
	}{
		{`{"username":"alice","uid":1026,"gid":100,"home":"/var/services/homes/alice","admin":false,"expired":"normal","groups":[100]}` + "\n", nil, true, false},
		{`{"username":"alice","uid":1026,"expired":"now","groups":[100]}`, nil, false, false},
		{`{"username":"bob","uid":1026,"expired":"normal"}`, nil, false, true},
		{`{"username":"alice","uid":100,"expired":"normal"}`, nil, false, true},
		{`{"username":"alice","uid":1026,"expired":"later"}`, nil, false, true},
		{`{"username":"alice","uid":1026,"expired":"normal"}{}`, nil, false, true},
		{`not json`, nil, false, true},
		{`{"username":"alice","uid":1026,"expired":"normal"}`, errors.New("exit 2"), false, true},
	} {
		enabled, err := parseNativeAccount([]byte(tc.out), "alice", tc.err)
		if enabled != tc.enabled || (err != nil) != tc.fails {
			t.Errorf("%s: %v %v", tc.out, enabled, err)
		}
	}
}
