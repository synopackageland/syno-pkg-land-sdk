package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const integrationPolicy = `{"schema":1,"default":"administrators","methods":{` +
	`"app.callback":"integration","app.upload":"authenticated","app.proxy":"authenticated","app.read":"authenticated","app.admin":"administrators"}}`

// admitter models dsm.EntryGate: it refuses accounts the gate would refuse.
type admitter struct {
	entryAuth
	refuse error
	admin  bool
	calls  []string
}

func (a *admitter) AdmitPrincipal(_ context.Context, user string) (Identity, error) {
	a.calls = append(a.calls, user)
	if a.refuse != nil {
		return Identity{}, a.refuse
	}
	return Identity{User: user, Admin: a.admin}, nil
}

func integrationServer(t *testing.T, auth Authenticator, callback http.Handler) *Server {
	t.Helper()
	ok := func(context.Context, Caller, json.RawMessage) (any, error) { return "ran", nil }
	srv, err := NewServerWithPermissions(auth, []byte(integrationPolicy),
		HandleIntegration("app.callback", callback),
		HandleStream("app.upload", func(_ context.Context, _ Caller, w http.ResponseWriter, _ *http.Request) error {
			w.Write([]byte("uploaded"))
			return nil
		}),
		HandleHTTP("/proxy/", "app.proxy", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("proxied")) })),
		Handle("app.read", ok),
		Handle("app.admin", ok))
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestOnlyTheListedIntegrationMethodSkipsTheSession(t *testing.T) {
	auth := &admitter{entryAuth: entryAuth{&fakeAuth{err: ErrNoSession}, "app-privilege"}}
	ran := 0
	srv := integrationServer(t, auth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran++
		if HTTPCaller(r.Context()).Authenticated() {
			t.Error("integration handler received a verified caller")
		}
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	}))
	r := httptest.NewRequest("POST", "/api.cgi?method=app.callback", strings.NewReader("token=x"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "token=x" || ran != 1 {
		t.Fatalf("integration callback: %d %q ran=%d", w.Code, w.Body, ran)
	}

	// Every other method, whatever its transport, still needs a DSM session.
	for _, tc := range []struct{ target, body string }{
		{"/api.cgi?method=app.upload", ""},
		{"/api.cgi?method=app.read", ""},
		{"/api.cgi?method=app.admin", ""},
		{"/proxy/item", ""},
		{"/api.cgi", `{"method":"app.read","params":{}}`},
		{"/api.cgi", `{"method":"app.admin","params":{}}`},
		{"/api.cgi", `{"v":1,"calls":[{"method":"app.read","params":{}}]}`},
	} {
		r := httptest.NewRequest("POST", tc.target, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), "ran") || strings.Contains(w.Body.String(), "uploaded") || strings.Contains(w.Body.String(), "proxied") || !strings.Contains(w.Body.String(), "session_required") {
			t.Fatalf("%s %s bypassed the session: %d %s", tc.target, tc.body, w.Code, w.Body)
		}
	}
	if ran != 1 {
		t.Fatal("integration handler ran for another method")
	}
}

func TestIntegrationMethodIsNotReachableAnotherWay(t *testing.T) {
	auth := &admitter{entryAuth: entryAuth{&fakeAuth{user: "operator", admin: true}, "app-privilege"}}
	ran := 0
	srv := integrationServer(t, auth, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran++ }))
	for _, tc := range []struct {
		target, body string
		headers      map[string]string
		code         int
	}{
		// JSON and batch calls cannot name it, even for an administrator.
		{"/api.cgi", `{"method":"app.callback","params":{}}`, nil, 200},
		{"/api.cgi", `{"v":1,"calls":[{"method":"app.callback","params":{}}]}`, nil, 200},
		// Repeated selectors are refused instead of picking one.
		{"/api.cgi?method=app.callback&method=app.read", "", nil, 400},
		{"/api.cgi?method=app.read&method=app.callback", "", nil, 400},
		{"/api.cgi?method=app.callback&x=%zz", "", nil, 400},
		// A browser posting cross-site is refused before the handler.
		{"/api.cgi?method=app.callback", "", map[string]string{"Origin": "https://evil.example"}, 403},
		{"/api.cgi?method=app.callback", "", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
	} {
		r := httptest.NewRequest("POST", tc.target, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		if tc.headers == nil {
			r.Header.Set("Sec-Fetch-Site", "same-origin")
		}
		for k, v := range tc.headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != tc.code || ran != 0 {
			t.Fatalf("%s %s: %d %s ran=%d", tc.target, tc.body, w.Code, w.Body, ran)
		}
		if tc.code == 200 && !strings.Contains(w.Body.String(), "unknown_method") {
			t.Fatalf("%s reached integration route: %s", tc.body, w.Body)
		}
	}
}

func TestIntegrationPermissionRequiresHandleIntegration(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	ok := func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil }
	auth := entryAuth{&fakeAuth{}, "app-privilege"}
	for name, tc := range map[string]struct {
		policy string
		route  Route
	}{
		"json route":            {`"app.x":"integration"`, Handle("app.x", ok)},
		"stream route":          {`"app.x":"integration"`, HandleStream("app.x", func(context.Context, Caller, http.ResponseWriter, *http.Request) error { return nil })},
		"prefix route":          {`"app.x":"integration"`, HandleHTTP("/x/", "app.x", handler)},
		"authenticated":         {`"app.x":"authenticated"`, HandleIntegration("app.x", handler)},
		"public":                {`"app.x":"public"`, HandleIntegration("app.x", handler)},
		"administrators":        {`"app.x":"administrators"`, HandleIntegration("app.x", handler)},
		"undeclared":            {`"app.y":"authenticated"`, HandleIntegration("app.x", handler)},
		"nil handler":           {`"app.x":"integration"`, HandleIntegration("app.x", nil)},
		"sdk method":            {``, HandleIntegration("identity.status", handler)},
		"unlisted default only": {`"app.y":"integration"`, Handle("app.y", ok)},
	} {
		raw := `{"schema":1,"default":"administrators","methods":{` + tc.policy + `}}`
		routes := []Route{tc.route}
		if strings.Contains(tc.policy, "app.y") && tc.route.method != "app.y" {
			routes = append(routes, Handle("app.y", ok))
		}
		if _, err := NewServerWithPermissions(auth, []byte(raw), routes...); err == nil {
			t.Errorf("%s: server accepted the route", name)
		}
	}
}

func TestIntegrationRoutesShareTheHTTPInventoryDigest(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	routes := []Route{HandleIntegration("app.callback", handler), HandleHTTP("/proxy/", "app.proxy", handler)}
	defer func(saved string) { expectedHTTPRoutesDigest = saved }(expectedHTTPRoutesDigest)
	// sha256 of [{"prefix":"/proxy/","method":"app.proxy"},{"prefix":"?method=app.callback","method":"app.callback"}]
	raw := `[{"prefix":"/proxy/","method":"app.proxy"},{"prefix":"?method=app.callback","method":"app.callback"}]`
	expectedHTTPRoutesDigest = sha256Hex(raw)
	policy := []byte(`{"schema":1,"default":"administrators","methods":{"app.callback":"integration","app.proxy":"authenticated"}}`)
	auth := entryAuth{&fakeAuth{}, "app-privilege"}
	if _, err := NewServerWithPermissions(auth, policy, routes...); err != nil {
		t.Fatalf("matching inventory refused: %v", err)
	}
	expectedHTTPRoutesDigest = sha256Hex(`[{"prefix":"/proxy/","method":"app.proxy"}]`)
	if _, err := NewServerWithPermissions(auth, policy, routes...); err == nil {
		t.Fatal("an integration callback missing from the bound inventory was accepted")
	}
	expectedHTTPRoutesDigest = sha256Hex(raw)
	described, err := DescribeRoutes(policy, routes...)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range described {
		if d["method"] == "app.callback" && (d["kind"] != "integration" || d["permission"] != "integration" || d["prefix"] != "") {
			t.Fatalf("integration description: %v", d)
		}
	}
}

func TestActAsRunsTheEntryGateAndNeverConfersAdmin(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   string
		refuse error
		want   error
	}{
		{"allowed", "app-privilege", nil, nil},
		{"disabled", "app-privilege", ErrAccountDisabled, ErrAccountDisabled},
		{"no application privilege", "app-privilege", ErrAppPrivilegeDenied, ErrAppPrivilegeDenied},
		{"admin-only app", "admin-only", nil, ErrAdminRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &admitter{entryAuth: entryAuth{&fakeAuth{err: ErrNoSession}, tc.mode}, refuse: tc.refuse, admin: true}
			var caller Caller
			var actErr error
			srv := integrationServer(t, auth, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				caller, actErr = ActAs(r.Context(), r, "alice")
			}))
			r := httptest.NewRequest("POST", "/api.cgi?method=app.callback", nil)
			srv.ServeHTTP(httptest.NewRecorder(), r)
			if !errors.Is(actErr, tc.want) {
				t.Fatalf("ActAs error = %v, want %v", actErr, tc.want)
			}
			if tc.want != nil {
				if caller.Authenticated() || caller.User() != "" {
					t.Fatalf("refused ActAs returned a caller: %+v", caller)
				}
				return
			}
			if !caller.Authenticated() || caller.User() != "alice" || caller.Admin() {
				t.Fatalf("ActAs caller = %+v", caller)
			}
			if len(auth.calls) != 1 || auth.calls[0] != "alice" {
				t.Fatalf("entry gate not asked about the account: %v", auth.calls)
			}
		})
	}
}

func TestActAsOnlyInsideAnIntegrationHandler(t *testing.T) {
	r := httptest.NewRequest("POST", "/api.cgi?method=app.callback", nil)
	if _, err := ActAs(context.Background(), r, "alice"); !errors.Is(err, ErrNotIntegration) {
		t.Fatalf("ActAs outside a handler: %v", err)
	}
	// A server without an admitting entry gate cannot act for an account.
	srv := integrationServer(t, &fakeAuth{err: ErrNoSession}, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if c, err := ActAs(r.Context(), r, "alice"); err == nil || c.Authenticated() {
			t.Errorf("ActAs without an entry gate: %+v %v", c, err)
		}
	}))
	srv.ServeHTTP(httptest.NewRecorder(), r)
	var stream error
	srv = integrationServer(t, &admitter{entryAuth: entryAuth{&fakeAuth{user: "bob"}, "app-privilege"}}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.routes["app.upload"] = Route{method: "app.upload", policy: policyUser, streamHandler: func(_ context.Context, _ Caller, _ http.ResponseWriter, r *http.Request) error {
		_, stream = ActAs(r.Context(), r, "alice")
		return nil
	}}
	up := httptest.NewRequest("POST", "/api.cgi?method=app.upload", nil)
	up.Header.Set("Sec-Fetch-Site", "same-origin")
	srv.ServeHTTP(httptest.NewRecorder(), up)
	if !errors.Is(stream, ErrNotIntegration) {
		t.Fatalf("ActAs inside a session route: %v", stream)
	}
}

func sha256Hex(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
