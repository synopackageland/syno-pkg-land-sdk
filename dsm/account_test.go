package dsm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
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

const normalAccountJSON = `{"username":"alice","uid":1026,"gid":100,"home":"/var/services/homes/alice","admin":false,"expired":"normal","groups":[100],"source":"local"}`

func TestParseAccountInfo(t *testing.T) {
	for _, tc := range []struct {
		out     string
		err     error
		expired string
		fails   bool
	}{
		{normalAccountJSON + "\n", nil, "normal", false},
		{strings.ReplaceAll(normalAccountJSON, `"normal"`, `"now"`), nil, "now", false},
		{strings.ReplaceAll(normalAccountJSON, `"alice"`, `"bob"`), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `1026`, `100`), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `"normal"`, `"later"`), nil, "", true},
		{normalAccountJSON + `{}`, nil, "", true},
		{normalAccountJSON + `false`, nil, "", true},
		{normalAccountJSON + `garbage`, nil, "", true},
		{`not json`, nil, "", true},
		{normalAccountJSON, errors.New("exit 2"), "", true},
		{strings.ReplaceAll(normalAccountJSON, `"admin":false,`, ``), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `"gid":100,`, ``), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `"local"`, `"ldap"`), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `"local"`, `"unknown"`), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `1026`, `4294967295`), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `1026`, `4294967296`), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `[100]`, `[]`), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `[100]`, `[101]`), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `[100]`, `[100,-1]`), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `[100]`, `[100,null]`), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `[100]`, `[100,4294967295]`), nil, "", true},
		{strings.ReplaceAll(normalAccountJSON, `1026`, `null`), nil, "", true},
	} {
		account, err := parseAccountInfo([]byte(tc.out), "alice", tc.err)
		if account.Expired != tc.expired || (err != nil) != tc.fails ||
			(tc.fails && !reflect.DeepEqual(account, AccountInfo{})) {
			t.Errorf("%s: %+v %v", tc.out, account, err)
		}
	}
}

func TestLookupAccountPreservesFullResult(t *testing.T) {
	raw := `{"username":"DOMAIN\\alice","uid":2969567732,"gid":2969567745,"home":"/home/alice","admin":true,"expired":"now","groups":[2969567745,101],"source":"domain"}`
	account, err := lookupAccount(context.Background(), `DOMAIN\alice`, func(_ context.Context, name string) ([]byte, error) {
		if name != `DOMAIN\alice` {
			t.Fatal(name)
		}
		return []byte(raw), nil
	})
	want := AccountInfo{Exists: true, Username: `DOMAIN\alice`, UID: 2969567732, GID: 2969567745,
		Home: "/home/alice", Admin: true, Expired: "now", Groups: []uint32{2969567745, 101}, Source: AccountSourceDomain}
	if err != nil || !reflect.DeepEqual(account, want) {
		t.Fatal(account, err)
	}
}

func TestLookupAccountDeadlineAndCancellation(t *testing.T) {
	if DefaultAccountLookupTimeout != runtimebudget.Operation {
		t.Fatal("native lookup must use the shared operation default")
	}
	for _, timeout := range []time.Duration{0, time.Second, time.Minute, 10 * time.Minute} {
		ctx := context.Background()
		if timeout != 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		start := time.Now()
		_, err := lookupAccount(ctx, "alice", func(ctx context.Context, _ string) ([]byte, error) {
			deadline, ok := ctx.Deadline()
			want := timeout
			if want == 0 {
				want = DefaultAccountLookupTimeout
			}
			if !ok || deadline.Sub(start) < want-100*time.Millisecond || deadline.Sub(start) > want+100*time.Millisecond {
				t.Fatalf("deadline %v; want %v", deadline.Sub(start), want)
			}
			return []byte(normalAccountJSON), nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := error(context.Canceled)
		if deadline {
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = ErrAccountTimeout
		}
		cancel()
		account, err := lookupAccount(ctx, "alice", func(context.Context, string) ([]byte, error) {
			t.Fatal("canceled lookup started")
			return nil, nil
		})
		if !errors.Is(err, want) || account.Exists || (deadline && !errors.Is(err, context.DeadlineExceeded)) {
			t.Fatal(account, err)
		}
	}
	for _, name := range []string{"", " alice", "a/b", "a\x00b"} {
		_, err := lookupAccount(context.Background(), name, func(context.Context, string) ([]byte, error) {
			t.Fatal("invalid name reached bridge")
			return nil, nil
		})
		if !errors.Is(err, ErrAccountUnavailable) {
			t.Fatal(err)
		}
	}
}

func TestAdmitAdministratorAsksDSMNotTheApp(t *testing.T) {
	unavailable := errors.New("role provider unavailable")
	for _, tc := range []struct {
		name           string
		mode           string
		admin, enabled bool
		adminErr       error
		want           error
	}{
		{"administrator", EntryAppPrivilege, true, true, nil, nil},
		{"administrator of an admin-only app", EntryAdminOnly, true, true, nil, nil},
		{"not an administrator", EntryAppPrivilege, false, true, nil, api.ErrAdminRequired},
		{"disabled administrator", EntryAppPrivilege, true, false, nil, api.ErrAccountDisabled},
		{"role provider unavailable", EntryAppPrivilege, true, true, unavailable, unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The session authenticator would vouch for anyone; it is never asked.
			gate, _ := newTestGate(t, tc.mode, fixedAuth{id: api.Identity{User: "alice", Admin: true}}, &lookup{allowed: true})
			gate.admin = func(_ context.Context, user string) (bool, error) {
				if user != "alice" {
					t.Errorf("role check for %q", user)
				}
				return tc.admin, tc.adminErr
			}
			gate.enabled = func(context.Context, string) (bool, error) { return tc.enabled, nil }
			id, err := gate.AdmitAdministrator(context.Background(), "alice")
			if !errors.Is(err, tc.want) || (tc.want == nil) != (id.Admin && id.User == "alice") {
				t.Fatalf("AdmitAdministrator = %+v, %v", id, err)
			}
		})
	}
	gate, _ := newTestGate(t, EntryAppPrivilege, ordinary("unused"), &lookup{allowed: true})
	gate.admin = func(context.Context, string) (bool, error) { t.Fatal("role provider ran"); return true, nil }
	for _, name := range []string{"", " alice", "a/b", "a\x00b"} {
		if _, err := gate.AdmitAdministrator(context.Background(), name); err == nil {
			t.Fatalf("admitted %q", name)
		}
	}
}
