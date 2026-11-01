package dsm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type fixedAuth struct {
	id  api.Identity
	err error
}

func (f fixedAuth) Authenticate(*http.Request) (api.Identity, error) { return f.id, f.err }

type lookup struct {
	calls         int
	user, app, ip string
	allowed       bool
	err           error
}

func (l *lookup) check(_ context.Context, user, app, ip string) (bool, error) {
	l.calls++
	l.user, l.app, l.ip = user, app, ip
	return l.allowed && l.err == nil, l.err
}

func newTestGate(t *testing.T, mode string, next api.Authenticator, l *lookup) (*EntryGate, *time.Time) {
	t.Helper()
	auth, err := NewEntryGate(next, mode, "DemoApp", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gate := auth.(*EntryGate)
	now := time.Unix(1_700_000_000, 0)
	gate.now = func() time.Time { return now }
	gate.allowed = l.check
	return gate, &now
}

func ordinary(user string) fixedAuth { return fixedAuth{id: api.Identity{User: user}} }

func TestUndeclaredEntryKeepsTheAuthenticatorUnchanged(t *testing.T) {
	next := ordinary("bob")
	got, err := NewEntryGate(next, "", "DemoApp", t.TempDir())
	if err != nil || got != api.Authenticator(next) {
		t.Fatalf("legacy entry wrapped the authenticator: %T %v", got, err)
	}
}

func TestUnknownEntryModeFailsAtStartup(t *testing.T) {
	for _, mode := range []string{"all-users", "users", "App-Privilege", "admin"} {
		if _, err := NewEntryGate(ordinary("bob"), mode, "DemoApp", t.TempDir()); err == nil {
			t.Fatalf("accepted entry mode %q", mode)
		}
	}
	if _, err := NewEntryGate(nil, EntryAppPrivilege, "DemoApp", t.TempDir()); err == nil {
		t.Fatal("accepted a missing authenticator")
	}
	if _, err := NewEntryGate(ordinary("bob"), EntryAppPrivilege, "Demo.App", t.TempDir()); err == nil {
		t.Fatal("accepted an invalid package ID")
	}
}

func TestAdministratorsEnterWithoutAppPrivilegeLookup(t *testing.T) {
	for _, mode := range []string{EntryAdminOnly, EntryAppPrivilege} {
		l := &lookup{}
		gate, _ := newTestGate(t, mode, fixedAuth{id: api.Identity{User: "alice", Admin: true}}, l)
		id, err := gate.Authenticate(sessionRequest())
		if err != nil || !id.Admin || id.User != "alice" || l.calls != 0 {
			t.Fatalf("%s: %+v %v calls=%d", mode, id, err, l.calls)
		}
	}
}

func TestAdminOnlyEntryRefusesOrdinaryUsers(t *testing.T) {
	l := &lookup{allowed: true}
	gate, _ := newTestGate(t, EntryAdminOnly, ordinary("bob"), l)
	if _, err := gate.Authenticate(sessionRequest()); !errors.Is(err, api.ErrAdminRequired) || l.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, l.calls)
	}
}

func TestAppPrivilegeEntryAsksDSMForTheRegisteredAppAndClientAddress(t *testing.T) {
	l := &lookup{allowed: true}
	gate, _ := newTestGate(t, EntryAppPrivilege, ordinary(`CORP\bob`), l)
	id, err := gate.Authenticate(sessionRequest())
	if err != nil || id.User != `CORP\bob` || id.Admin {
		t.Fatalf("%+v %v", id, err)
	}
	if l.user != `CORP\bob` || l.app != "SYNO.SDS.DemoApp.Instance" || l.ip != "192.0.2.7" {
		t.Fatalf("lookup got user=%q app=%q ip=%q", l.user, l.app, l.ip)
	}
}

func TestAppPrivilegeDenialAndLookupFailureBothRefuse(t *testing.T) {
	gate, _ := newTestGate(t, EntryAppPrivilege, ordinary("bob"), &lookup{allowed: false})
	if _, err := gate.Authenticate(sessionRequest()); !errors.Is(err, api.ErrAppPrivilegeDenied) {
		t.Fatalf("denied: %v", err)
	}
	gate, _ = newTestGate(t, EntryAppPrivilege, ordinary("bob"), &lookup{err: errors.New("bridge exited 2")})
	_, err := gate.Authenticate(sessionRequest())
	if err == nil || errors.Is(err, api.ErrAppPrivilegeDenied) || errors.Is(err, api.ErrNoSession) {
		t.Fatalf("lookup failure must surface as unavailable: %v", err)
	}
}

func TestUnparseableClientAddressRefusesWithoutLookup(t *testing.T) {
	for _, addr := range []string{"", "not-an-ip", "192.0.2.300:1", "[::1"} {
		l := &lookup{allowed: true}
		gate, _ := newTestGate(t, EntryAppPrivilege, ordinary("bob"), l)
		r := sessionRequest()
		r.RemoteAddr = addr
		if _, err := gate.Authenticate(r); err == nil || l.calls != 0 {
			t.Fatalf("%q: err=%v calls=%d", addr, err, l.calls)
		}
	}
	l := &lookup{allowed: true}
	gate, _ := newTestGate(t, EntryAppPrivilege, ordinary("bob"), l)
	r := sessionRequest()
	r.RemoteAddr = "[2001:db8::7]:443"
	if _, err := gate.Authenticate(r); err != nil || l.ip != "2001:db8::7" {
		t.Fatalf("IPv6: %v %q", err, l.ip)
	}
}

func TestSessionAndAuthenticationFailuresPassThroughUnchanged(t *testing.T) {
	for _, want := range []error{api.ErrNoSession, errors.New("authenticate.cgi unavailable")} {
		l := &lookup{allowed: true}
		gate, _ := newTestGate(t, EntryAppPrivilege, fixedAuth{err: want}, l)
		if _, err := gate.Authenticate(sessionRequest()); !errors.Is(err, want) || l.calls != 0 {
			t.Fatalf("err=%v calls=%d", err, l.calls)
		}
	}
}

func TestDecisionsAreCachedBrieflyPerUserAppAndAddress(t *testing.T) {
	l := &lookup{allowed: true}
	gate, now := newTestGate(t, EntryAppPrivilege, ordinary("bob"), l)
	for range 3 {
		if _, err := gate.Authenticate(sessionRequest()); err != nil {
			t.Fatal(err)
		}
	}
	if l.calls != 1 {
		t.Fatalf("cached decision looked up %d times", l.calls)
	}
	l.allowed = false
	*now = now.Add(EntryCacheTTL - time.Second)
	if _, err := gate.Authenticate(sessionRequest()); err != nil || l.calls != 1 {
		t.Fatalf("within TTL: %v calls=%d", err, l.calls)
	}
	*now = now.Add(2 * time.Second)
	if _, err := gate.Authenticate(sessionRequest()); !errors.Is(err, api.ErrAppPrivilegeDenied) || l.calls != 2 {
		t.Fatalf("after TTL: %v calls=%d", err, l.calls)
	}
	r := sessionRequest()
	r.RemoteAddr = "198.51.100.4:1"
	gate.Authenticate(r)
	if l.calls != 3 {
		t.Fatalf("another address reused a cached decision: calls=%d", l.calls)
	}
}

func TestCacheSurvivesAcrossCGIProcessesAndIgnoresFutureEntries(t *testing.T) {
	dir := t.TempDir()
	l := &lookup{allowed: true}
	first, err := NewEntryGate(ordinary("bob"), EntryAppPrivilege, "DemoApp", dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	g1 := first.(*EntryGate)
	g1.now = func() time.Time { return now }
	g1.allowed = l.check
	g1.Authenticate(sessionRequest())
	second, _ := NewEntryGate(ordinary("bob"), EntryAppPrivilege, "DemoApp", dir)
	g2 := second.(*EntryGate)
	g2.now = func() time.Time { return now.Add(time.Second) }
	g2.allowed = g1.allowed
	g2.Authenticate(sessionRequest())
	if l.calls != 1 {
		t.Fatalf("a new process ignored the shared cache: calls=%d", l.calls)
	}
	g2.now = func() time.Time { return now.Add(-time.Minute) }
	g2.Authenticate(sessionRequest())
	if l.calls != 2 {
		t.Fatalf("an entry from the future was trusted: calls=%d", l.calls)
	}
	info, err := os.Stat(filepath.Join(dir, "entry", entryCacheFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache file: %v %v", info, err)
	}
}

func TestFailuresAreNeverCached(t *testing.T) {
	l := &lookup{err: errors.New("bridge exited 2")}
	gate, _ := newTestGate(t, EntryAppPrivilege, ordinary("bob"), l)
	gate.Authenticate(sessionRequest())
	l.err, l.allowed = nil, true
	if _, err := gate.Authenticate(sessionRequest()); err != nil || l.calls != 2 {
		t.Fatalf("err=%v calls=%d", err, l.calls)
	}
}

func TestCacheIsBoundedAndToleratesCorruption(t *testing.T) {
	l := &lookup{allowed: true}
	gate, _ := newTestGate(t, EntryAppPrivilege, ordinary("bob"), l)
	for i := range EntryCacheMaxEntries + 20 {
		r := sessionRequest()
		r.RemoteAddr = "10.0." + strconv.Itoa(i/250) + "." + strconv.Itoa(i%250) + ":1"
		if _, err := gate.Authenticate(r); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := gate.readCache()
	if err != nil || len(entries) > EntryCacheMaxEntries {
		t.Fatalf("cache holds %d entries: %v", len(entries), err)
	}
	if err := os.WriteFile(filepath.Join(gate.dir, entryCacheFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := l.calls
	if _, err := gate.Authenticate(sessionRequest()); err != nil || l.calls != before+1 {
		t.Fatalf("corrupt cache: %v calls=%d", err, l.calls-before)
	}
}

func TestUnwritableCacheStillDecides(t *testing.T) {
	l := &lookup{allowed: false}
	gate, _ := newTestGate(t, EntryAppPrivilege, ordinary("bob"), l)
	gate.dir = filepath.Join(t.TempDir(), "missing", "nested")
	if _, err := gate.Authenticate(sessionRequest()); !errors.Is(err, api.ErrAppPrivilegeDenied) {
		t.Fatal(err)
	}
}

func TestAppPrivilegeRefusesMalformedInputsBeforeTheBridge(t *testing.T) {
	for _, app := range []string{"", "SYNO", "SYNO.SDS.Demo App.Instance", "SYNO..Instance", "SYNO.SDS.Demo.Instance\n"} {
		if appID.MatchString(app) {
			t.Fatalf("accepted app ID %q", app)
		}
	}
	if !appID.MatchString("SYNO.SDS.DemoApp.Instance") {
		t.Fatal("refused the generated app ID")
	}
	for _, args := range [][3]string{{"", "SYNO.SDS.Demo.Instance", "192.0.2.5"}, {"bob", "SYNO.SDS.Demo.Instance", "192.0.2.05"}, {"bob", "SYNO.SDS.Demo.Instance", "fe80::1%eth0"}, {"bob", "bad", "192.0.2.5"}} {
		if ok, err := AppPrivilege(context.Background(), args[0], args[1], args[2]); ok || err == nil {
			t.Fatalf("%q: %v %v", args, ok, err)
		}
	}
}

// admin-only is the floor of every route: SDK routes the policy marks
// authenticated (nas.files.list, bg.jobs.submit, delegate.status) refuse an
// ordinary user exactly like app routes, while administrators reach them.
func TestAdminOnlyEntryFloorsSDKRoutesAtAdministrators(t *testing.T) {
	sdkRoutes := []string{"nas.files.list", "bg.jobs.submit", "delegate.status", "portal.status", "executor.status"}
	for _, tc := range []struct {
		id   api.Identity
		code string
	}{
		{api.Identity{User: "bob"}, "admin_required"},
		{api.Identity{User: "alice", Admin: true}, ""},
	} {
		executed := 0
		var routes []api.Route
		for _, method := range sdkRoutes {
			routes = append(routes, api.Handle(method, func(context.Context, api.Caller, json.RawMessage) (any, error) {
				executed++
				return map[string]bool{"ok": true}, nil
			}))
		}
		gate, _ := newTestGate(t, EntryAdminOnly, fixedAuth{id: tc.id}, &lookup{allowed: true})
		srv, err := api.NewServerWithPermissions(gate, []byte(`{"schema":1,"default":"administrators","methods":{}}`), routes...)
		if err != nil {
			t.Fatal(err)
		}
		for _, method := range sdkRoutes {
			req := httptest.NewRequest(http.MethodPost, "http://nas.local/webman/3rdparty/DemoApp/cgi/api.cgi", strings.NewReader(`{"method":"`+method+`"}`))
			req.Header.Set("Origin", "http://"+req.Host)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			var env struct {
				Error *struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("%s: %q", method, rec.Body.String())
			}
			got := ""
			if env.Error != nil {
				got = env.Error.Code
			}
			if got != tc.code {
				t.Fatalf("%s as %+v: code %q, want %q", method, tc.id, got, tc.code)
			}
		}
		if want := map[bool]int{true: len(sdkRoutes), false: 0}[tc.id.Admin]; executed != want {
			t.Fatalf("%+v executed %d handlers, want %d", tc.id, executed, want)
		}
	}
}

// An observed request learns which verified account the gate refused; the
// refusal itself is unchanged.
func TestEntryGateReportsTheRefusedAccountToAnObserver(t *testing.T) {
	for _, c := range []struct {
		mode string
		err  error
	}{{EntryAdminOnly, api.ErrAdminRequired}, {EntryAppPrivilege, api.ErrAppPrivilegeDenied}} {
		gate, _ := newTestGate(t, c.mode, ordinary("bob"), &lookup{allowed: false})
		route := api.Handle("demo.read", func(context.Context, api.Caller, json.RawMessage) (any, error) { return "ok", nil })
		srv, err := api.NewServerWithPermissions(gate, []byte(`{"schema":1,"default":"administrators","methods":{"demo.read":"authenticated"}}`), route)
		if err != nil {
			t.Fatal(err)
		}
		var got api.Outcome
		h := api.WithObserver(srv, func(_ *http.Request, o api.Outcome) { got = o })
		req := httptest.NewRequest(http.MethodPost, "http://nas.local/api.cgi", strings.NewReader(`{"method":"demo.read","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://nas.local")
		req.RemoteAddr = "192.0.2.4:1234"
		h.ServeHTTP(httptest.NewRecorder(), req)
		want := "admin_required"
		if errors.Is(c.err, api.ErrAppPrivilegeDenied) {
			want = "app_privilege_denied"
		}
		if got.User != "bob" || got.Code != want || got.Admin {
			t.Fatalf("%s: %+v", c.mode, got)
		}
	}
}
