package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// refusingGate verifies the account, records it, then refuses it, as
// dsm.EntryGate does for a non-administrator.
type refusingGate struct {
	user string
	err  error
}

func (g refusingGate) Authenticate(r *http.Request) (Identity, error) {
	ObserveIdentity(r, Identity{User: g.user})
	return Identity{}, g.err
}

func observed(t *testing.T, srv http.Handler, body string, mutate ...func(*http.Request)) (Outcome, *httptest.ResponseRecorder) {
	t.Helper()
	var got []Outcome
	h := WithObserver(srv, func(_ *http.Request, o Outcome) { got = append(got, o) })
	rec, _ := post(t, h, body, mutate...)
	if len(got) != 1 {
		t.Fatalf("observer ran %d times", len(got))
	}
	return got[0], rec
}

func TestObserverReportsASuccessfulCall(t *testing.T) {
	n := 0
	srv := newTestServer(t, &fakeAuth{user: "alice", admin: true}, countingRoutes(&n)...)
	o, _ := observed(t, srv, `{"method":"demo.admin","params":{}}`)
	if o.Method != "demo.admin" || o.User != "alice" || !o.Admin || o.Code != "" || o.Status != http.StatusOK || o.Duration <= 0 {
		t.Fatalf("%+v", o)
	}
}

func TestObserverReportsRefusalsBeforeAnyRoute(t *testing.T) {
	n := 0
	cases := []struct {
		name string
		auth Authenticator
		body string
		code string
		user string
	}{
		{"no session", &fakeAuth{err: ErrNoSession}, `{"method":"demo.admin","params":{}}`, "session_required", ""},
		{"not administrator", &fakeAuth{user: "bob"}, `{"method":"demo.admin","params":{}}`, "admin_required", "bob"},
		{"entry refuses", refusingGate{user: "carol", err: ErrAdminRequired}, `{"method":"demo.admin","params":{}}`, "admin_required", "carol"},
		{"app privilege", refusingGate{user: "dave", err: ErrAppPrivilegeDenied}, `{"method":"demo.admin","params":{}}`, "app_privilege_denied", "dave"},
		{"unknown method", &fakeAuth{user: "alice", admin: true}, `{"method":"demo.missing","params":{}}`, "unknown_method", "alice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, _ := observed(t, newTestServer(t, c.auth, countingRoutes(&n)...), c.body)
			if o.Code != c.code || o.User != c.user || o.Method != "demo.admin" && o.Method != "demo.missing" || o.Status != http.StatusOK {
				t.Fatalf("%+v", o)
			}
		})
	}
	if n != 0 {
		t.Fatal("a refused call reached its handler")
	}
}

func TestObserverReportsAHandlerError(t *testing.T) {
	srv := newTestServer(t, &fakeAuth{user: "alice", admin: true}, Handle("demo.admin", func(context.Context, Caller, json.RawMessage) (any, error) {
		return nil, &Error{"conflict", "changed"}
	}))
	if o, _ := observed(t, srv, `{"method":"demo.admin","params":{}}`); o.Code != "conflict" || o.User != "alice" {
		t.Fatalf("%+v", o)
	}
}

func TestObserverReportsBatchMethods(t *testing.T) {
	n := 0
	srv := newTestServer(t, &fakeAuth{user: "alice", admin: true}, countingRoutes(&n)...)
	o, _ := observed(t, srv, `{"v":1,"calls":[{"method":"demo.admin","params":{}},{"method":"demo.user","params":{}}]}`)
	if o.Method != "" || strings.Join(o.Batch, ",") != "demo.admin,demo.user" || o.User != "alice" || o.Code != "" {
		t.Fatalf("%+v", o)
	}
}

func TestObserverReportsHTTPRouteStatus(t *testing.T) {
	route := HandleHTTP("/files/", "demo.files", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	srv, err := NewServerWithPermissions(&fakeAuth{user: "alice", admin: true}, []byte(`{"schema":1,"default":"administrators","methods":{"demo.files":"administrators"}}`), route)
	if err != nil {
		t.Fatal(err)
	}
	var got Outcome
	h := WithObserver(srv, func(_ *http.Request, o Outcome) { got = o })
	req := httptest.NewRequest(http.MethodGet, "http://nas.local/files/a", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got.Method != "demo.files" || got.User != "alice" || got.Status != http.StatusTeapot || got.Code != "" {
		t.Fatalf("%+v", got)
	}
}

func TestObserverPanicsNeverChangeTheResponse(t *testing.T) {
	n := 0
	srv := newTestServer(t, &fakeAuth{user: "alice", admin: true}, countingRoutes(&n)...)
	h := WithObserver(srv, func(*http.Request, Outcome) { panic("observer bug") })
	rec, env := post(t, h, `{"method":"demo.admin","params":{}}`)
	if rec.Code != http.StatusOK || !env.Success || n != 1 {
		t.Fatalf("%d %+v", rec.Code, env)
	}
}

func TestObserveIdentityOutsideAnObserverIsHarmless(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ObserveIdentity(req, Identity{User: "x"})
	ObserveIdentity(nil, Identity{User: "x"})
	if errors.Is(nil, ErrNoSession) {
		t.Fatal("unreachable")
	}
}
