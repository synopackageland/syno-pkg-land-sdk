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

// adminAdmitter models dsm.EntryGate's DSM role check.
type adminAdmitter struct {
	entryAuth
	id    Identity
	err   error
	calls []string
}

func (a *adminAdmitter) AdmitAdministrator(_ context.Context, user string) (Identity, error) {
	a.calls = append(a.calls, user)
	return a.id, a.err
}

func invokeServer(t *testing.T, auth Authenticator, inner func(context.Context) error) (*Server, *Caller) {
	t.Helper()
	got := &Caller{}
	record := func(ctx context.Context, c Caller, params json.RawMessage) (any, error) {
		*got = c
		if inner != nil {
			return nil, inner(ctx)
		}
		return string(params), nil
	}
	srv, err := NewServerWithPermissions(auth, []byte(integrationPolicy),
		HandleIntegration("app.callback", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("integration handler ran") })),
		HandleStream("app.upload", func(context.Context, Caller, http.ResponseWriter, *http.Request) error {
			t.Error("stream handler ran")
			return nil
		}),
		HandleHTTP("/proxy/", "app.proxy", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("HTTP handler ran") })),
		Handle("app.read", record),
		Handle("app.admin", record))
	if err != nil {
		t.Fatal(err)
	}
	return srv, got
}

func TestInvokeAsAdministratorRunsTheRouteForAVerifiedAdministrator(t *testing.T) {
	auth := &adminAdmitter{entryAuth: entryAuth{&fakeAuth{err: ErrNoSession}, "app-privilege"}, id: Identity{User: "operator", Admin: true}}
	srv, got := invokeServer(t, auth, nil)
	out, err := srv.InvokeAsAdministrator(context.Background(), "operator", "app.admin", json.RawMessage(`{"x":1}`))
	if err != nil || out != `{"x":1}` || !got.Admin() || got.User() != "operator" || len(auth.calls) != 1 || auth.calls[0] != "operator" {
		t.Fatalf("out=%v err=%v caller=%+v calls=%v", out, err, got, auth.calls)
	}
}

func TestInvokeAsAdministratorRefusesAnyoneTheRoleProviderDoesNotConfirm(t *testing.T) {
	unavailable := errors.New("role provider unavailable")
	for name, tc := range map[string]struct {
		id   Identity
		err  error
		want error
	}{
		"not an administrator":    {Identity{User: "alice"}, nil, ErrAdminRequired},
		"refused":                 {Identity{}, ErrAccountDisabled, ErrAccountDisabled},
		"unavailable":             {Identity{}, unavailable, unavailable},
		"administrator elsewhere": {Identity{User: "root", Admin: true}, nil, ErrAdminRequired},
	} {
		t.Run(name, func(t *testing.T) {
			auth := &adminAdmitter{entryAuth: entryAuth{&fakeAuth{user: "x", admin: true}, "app-privilege"}, id: tc.id, err: tc.err}
			srv, got := invokeServer(t, auth, nil)
			if _, err := srv.InvokeAsAdministrator(context.Background(), "alice", "app.read", json.RawMessage(`{}`)); !errors.Is(err, tc.want) || got.Authenticated() {
				t.Fatalf("err=%v ran=%v", err, got.Authenticated())
			}
		})
	}
	// A session authenticator alone cannot vouch for an account without a session.
	srv, got := invokeServer(t, entryAuth{&fakeAuth{user: "operator", admin: true}, "app-privilege"}, nil)
	if _, err := srv.InvokeAsAdministrator(context.Background(), "operator", "app.read", json.RawMessage(`{}`)); err == nil || got.Authenticated() {
		t.Fatal("ran without an administrator admitter")
	}
}

func TestInvokeAsAdministratorReachesOnlyAppJSONRoutes(t *testing.T) {
	auth := &adminAdmitter{entryAuth: entryAuth{&fakeAuth{}, "app-privilege"}, id: Identity{User: "operator", Admin: true}}
	srv, got := invokeServer(t, auth, nil)
	for _, method := range []string{"app.callback", "app.upload", "app.proxy", "app.missing", "app.status"} {
		_, err := srv.InvokeAsAdministrator(context.Background(), "operator", method, json.RawMessage(`{}`))
		if e := asError(err); e == nil || e.Code != "unknown_method" {
			t.Fatalf("%s: %v", method, err)
		}
	}
	for _, params := range []string{``, `{`, `{}{}`, `{"x":"` + strings.Repeat("a", MaxRequestBytes) + `"}`} {
		if _, err := srv.InvokeAsAdministrator(context.Background(), "operator", "app.read", json.RawMessage(params)); err == nil {
			t.Fatalf("accepted params %.20q", params)
		}
	}
	if got.Authenticated() || len(auth.calls) != 0 {
		t.Fatalf("checked or ran before refusing: %v", auth.calls)
	}
}

func TestInvokeAsAdministratorRefusesCGIAndRequests(t *testing.T) {
	auth := &adminAdmitter{entryAuth: entryAuth{&fakeAuth{user: "alice"}, "app-privilege"}, id: Identity{User: "operator", Admin: true}}
	var srv *Server
	var nested error
	srv, _ = invokeServer(t, auth, func(ctx context.Context) error {
		if nested == nil {
			nested = errors.New("unset")
			_, nested = srv.InvokeAsAdministrator(ctx, "operator", "app.admin", json.RawMessage(`{}`))
		}
		return nil
	})
	// A request handler cannot pick an administrator to run as, even
	// through a context derived from its own invocation.
	r := httptest.NewRequest("POST", "/api.cgi", strings.NewReader(`{"method":"app.read","params":{}}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	srv.ServeHTTP(httptest.NewRecorder(), r)
	if !errors.Is(nested, ErrInsideDispatch) {
		t.Fatalf("from a request: %v", nested)
	}
	nested = nil
	if _, err := srv.InvokeAsAdministrator(context.Background(), "operator", "app.read", json.RawMessage(`{}`)); err != nil || !errors.Is(nested, ErrInsideDispatch) {
		t.Fatalf("nested invoke: %v %v", err, nested)
	}
	t.Setenv("GATEWAY_INTERFACE", "CGI/1.1")
	before := len(auth.calls)
	if _, err := srv.InvokeAsAdministrator(context.Background(), "operator", "app.read", json.RawMessage(`{}`)); !errors.Is(err, ErrInsideDispatch) || len(auth.calls) != before {
		t.Fatalf("under CGI: %v", err)
	}
}
