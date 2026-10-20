package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAuth returns a fixed outcome and counts how often it was consulted, so
// tests can prove each request authenticates at most once.
type fakeAuth struct {
	user  string
	admin bool
	err   error
	calls int
}

func (f *fakeAuth) Authenticate(*http.Request) (Identity, error) {
	f.calls++
	if f.err != nil {
		return Identity{}, f.err
	}
	return Identity{User: f.user, Admin: f.admin}, nil
}

func post(t *testing.T, srv http.Handler, body string, mutate ...func(*http.Request)) (*httptest.ResponseRecorder, envelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://nas.local/webman/3rdparty/App/cgi/api.cgi", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Every current browser sends Origin on a POST.
	req.Header.Set("Origin", "http://nas.local")
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not JSON: %q", rec.Body.String())
	}
	return rec, env
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func countingRoutes(counter *int) []Route {
	h := func(_ context.Context, c Caller, _ json.RawMessage) (any, error) {
		*counter++
		return map[string]string{"user": c.User()}, nil
	}
	return []Route{
		Handle("demo.user", h),
		Handle("demo.admin", h),
	}
}

func newTestServer(t *testing.T, auth Authenticator, routes ...Route) *Server {
	t.Helper()
	// Explicit fixture policy exercises opt-in user/public behavior. Production
	// defaults and constructor bypasses are covered independently.
	methods := map[string]string{}
	for _, route := range routes {
		switch route.Method() {
		case "demo.user", "demo.invalid", "demo.crash", "demo.panic":
			methods[route.Method()] = "authenticated"
		case "demo.ping":
			methods[route.Method()] = "public"
		}
	}
	raw, err := json.Marshal(map[string]any{"schema": 1, "default": "administrators", "methods": methods})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServerWithPermissions(auth, raw, routes...)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestUserRouteRunsForAuthenticatedUserWithVerifiedCaller(t *testing.T) {
	var executed int
	auth := &fakeAuth{user: "alice"}
	srv := newTestServer(t, auth, countingRoutes(&executed)...)

	rec, env := post(t, srv, `{"method":"demo.user"}`)

	if rec.Code != http.StatusOK || !env.Success {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if string(env.Data) != `{"user":"alice"}` {
		t.Fatalf("data = %s", env.Data)
	}
	if executed != 1 || auth.calls != 1 {
		t.Fatalf("executed=%d auth=%d, want 1/1", executed, auth.calls)
	}
}

func TestEveryProtectedRouteRefusesMissingInvalidAndUnavailableAuth(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"missing session", ErrNoSession, "session_required"},
		{"invalid session", ErrNoSession, "session_required"},
		{"auth unavailable", errors.New("authenticate.cgi crashed"), "auth_unavailable"},
	}
	for _, method := range []string{"demo.user", "demo.admin"} {
		for _, tc := range cases {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				var executed int
				auth := &fakeAuth{err: tc.err}
				srv := newTestServer(t, auth, countingRoutes(&executed)...)

				rec, env := post(t, srv, `{"method":"`+method+`"}`)

				if rec.Code != http.StatusOK || env.Success || env.Error.Code != tc.code {
					t.Fatalf("got %d %s", rec.Code, rec.Body.String())
				}
				if executed != 0 || auth.calls != 1 {
					t.Fatalf("executed=%d auth=%d, want 0/1", executed, auth.calls)
				}
			})
		}
	}
}

func TestAdminRouteRefusesNonAdminWithoutExecuting(t *testing.T) {
	var executed int
	srv := newTestServer(t, &fakeAuth{user: "bob"}, countingRoutes(&executed)...)

	rec, env := post(t, srv, `{"method":"demo.admin"}`)

	if rec.Code != http.StatusOK || env.Error.Code != "admin_required" || executed != 0 {
		t.Fatalf("got %d %s executed=%d", rec.Code, rec.Body.String(), executed)
	}
}

func TestAdminRouteRunsForAdmin(t *testing.T) {
	var executed int
	srv := newTestServer(t, &fakeAuth{user: "root-admin", admin: true}, countingRoutes(&executed)...)

	rec, _ := post(t, srv, `{"method":"demo.admin"}`)

	if rec.Code != http.StatusOK || executed != 1 {
		t.Fatalf("got %d executed=%d", rec.Code, executed)
	}
}

func TestUnknownMethodNeedsSessionThenIsNotFound(t *testing.T) {
	var executed int
	srv := newTestServer(t, &fakeAuth{err: ErrNoSession}, countingRoutes(&executed)...)
	_, env := post(t, srv, `{"method":"demo.missing"}`)
	if env.Error == nil || env.Error.Code != "session_required" {
		t.Fatalf("anonymous unknown method: got %+v, want session_required (no route probing)", env.Error)
	}

	srv = newTestServer(t, &fakeAuth{user: "alice", admin: true}, countingRoutes(&executed)...)
	rec, env := post(t, srv, `{"method":"demo.missing"}`)
	if rec.Code != http.StatusOK || env.Error.Code != "unknown_method" || executed != 0 {
		t.Fatalf("got %d %s executed=%d", rec.Code, rec.Body.String(), executed)
	}
}

func TestPublicRouteIsExplicitAndSkipsAuthentication(t *testing.T) {
	var executed int
	auth := &fakeAuth{err: ErrNoSession}
	srv := newTestServer(t, auth, Handle("demo.ping", func(_ context.Context, c Caller, _ json.RawMessage) (any, error) {
		executed++
		if c.Authenticated() {
			t.Error("public handler received an authenticated caller")
		}
		return "pong", nil
	}))

	rec, _ := post(t, srv, `{"method":"demo.ping"}`)

	if rec.Code != http.StatusOK || executed != 1 || auth.calls != 0 {
		t.Fatalf("got %d executed=%d auth=%d", rec.Code, executed, auth.calls)
	}
}

func TestTransportGuardsRunBeforeAuthenticationAndDispatch(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		mutate func(*http.Request)
		code   string
	}{
		{"GET", `{"method":"demo.user"}`, func(r *http.Request) { r.Method = http.MethodGet }, "method_not_allowed"},
		{"form post", `{"method":"demo.user"}`, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, "json_required"},
		{"cross-site fetch", `{"method":"demo.user"}`, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, "cross_origin"},
		{"foreign origin", `{"method":"demo.user"}`, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, "cross_origin"},
		{"oversized body", `{"method":"demo.user","params":"` + strings.Repeat("x", MaxRequestBytes) + `"}`, nil, "request_too_large"},
		{"malformed JSON", `{"method":`, nil, "bad_request"},
		{"unknown field", `{"method":"demo.user","user":"admin"}`, nil, "bad_request"},
		{"trailing data", `{"method":"demo.user"}{}`, nil, "bad_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var executed int
			auth := &fakeAuth{user: "alice", admin: true}
			srv := newTestServer(t, auth, countingRoutes(&executed)...)
			mutate := []func(*http.Request){}
			if tc.mutate != nil {
				mutate = append(mutate, tc.mutate)
			}

			rec, env := post(t, srv, tc.body, mutate...)

			if rec.Code != http.StatusOK || env.Error == nil || env.Error.Code != tc.code {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
			if executed != 0 || auth.calls != 0 {
				t.Fatalf("executed=%d auth=%d, want 0/0", executed, auth.calls)
			}
		})
	}
}

// countingReader records how much of the request body the server consumed.
type countingReader struct {
	r    io.Reader
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

// DSM's CGI gateway cuts the reply short when a CGI exits without reading
// its stdin, so every refusal still consumes the request body.
func TestRefusalsConsumeTheWholeRequestBody(t *testing.T) {
	cases := map[string]func(*http.Request){
		"form post":  func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
		"cross-site": func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"GET":        func(r *http.Request) { r.Method = http.MethodGet },
		"oversized":  func(*http.Request) {},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var executed int
			srv := newTestServer(t, &fakeAuth{user: "alice"}, countingRoutes(&executed)...)
			body := `{"method":"demo.user"}`
			if name == "oversized" {
				body = `{"method":"demo.user","params":"` + strings.Repeat("x", 2*MaxRequestBytes) + `"}`
			}
			reader := &countingReader{r: strings.NewReader(body)}
			req := httptest.NewRequest(http.MethodPost, "http://nas.local/api.cgi", reader)
			req.Header.Set("Content-Type", "application/json")
			mutate(req)
			srv.ServeHTTP(httptest.NewRecorder(), req)
			if reader.read != len(body) {
				t.Fatalf("read %d of %d body bytes", reader.read, len(body))
			}
		})
	}
}

func TestStateChangingRequestsNeedProofOfTheirOrigin(t *testing.T) {
	header := func(kv ...string) func(*http.Request) {
		return func(r *http.Request) {
			for i := 0; i < len(kv); i += 2 {
				r.Header.Del(kv[i])
				if kv[i+1] != "" {
					r.Header.Set(kv[i], kv[i+1])
				}
			}
		}
	}
	host := func(h string) func(*http.Request) { return func(r *http.Request) { r.Host = h } }
	cases := []struct {
		name   string
		mutate []func(*http.Request)
		code   string
	}{
		{"origin of the host", nil, ""},
		{"no origin, referer or fetch metadata", []func(*http.Request){header("Origin", "")}, "origin_required"},
		{"referer of the host", []func(*http.Request){header("Origin", "", "Referer", "http://nas.local/webman/3rdparty/App/index.html")}, ""},
		{"foreign referer", []func(*http.Request){header("Origin", "", "Referer", "https://evil.example/")}, "cross_origin"},
		{"opaque origin", []func(*http.Request){header("Origin", "null")}, "cross_origin"},
		{"same site, other port", []func(*http.Request){header("Sec-Fetch-Site", "same-site", "Origin", "http://nas.local:8443")}, "cross_origin"},
		{"user navigation", []func(*http.Request){header("Sec-Fetch-Site", "none")}, "cross_origin"},
		{"portal port", []func(*http.Request){host("nas.local:8443"), header("Origin", "https://nas.local:8443")}, ""},
		{"proxy forwards the host", []func(*http.Request){host("127.0.0.1:5000"), header("Origin", "https://nas.example.com", "X-Forwarded-Host", "nas.example.com")}, ""},
		{"proxy forwards another host", []func(*http.Request){host("127.0.0.1:5000"), header("Origin", "https://evil.example", "X-Forwarded-Host", "nas.example.com")}, "cross_origin"},
		{"proxy rewrites the host, browser attests same origin", []func(*http.Request){host("127.0.0.1:5000"), header("Origin", "https://nas.example.com", "Sec-Fetch-Site", "same-origin")}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var executed int
			srv := newTestServer(t, &fakeAuth{user: "alice"}, countingRoutes(&executed)...)
			rec, env := post(t, srv, `{"method":"demo.user"}`, tc.mutate...)
			got := ""
			if env.Error != nil {
				got = env.Error.Code
			}
			if got != tc.code {
				t.Fatalf("got %q, want %q: %s", got, tc.code, rec.Body.String())
			}
		})
	}
}

func TestTransfersNeedProofOfOriginOnlyToChangeState(t *testing.T) {
	stream := HandleStream("demo.file", func(context.Context, Caller, http.ResponseWriter, *http.Request) error { return nil })
	for method, code := range map[string]string{http.MethodGet: "", http.MethodPost: "origin_required"} {
		t.Run(method, func(t *testing.T) {
			srv := newTestServer(t, &fakeAuth{user: "alice", admin: true}, stream)
			r := httptest.NewRequest(method, "http://nas.local/api.cgi?method=demo.file", strings.NewReader("data"))
			r.Header.Set("Content-Type", "application/octet-stream")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			if refused := strings.Contains(w.Body.String(), `"origin_required"`); refused != (code != "") {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestSameOriginRequestIsAccepted(t *testing.T) {
	var executed int
	srv := newTestServer(t, &fakeAuth{user: "alice"}, countingRoutes(&executed)...)
	rec, _ := post(t, srv, `{"method":"demo.user"}`, func(r *http.Request) {
		r.Header.Set("Origin", "http://nas.local")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestResponsesAreUncachedJSON(t *testing.T) {
	var executed int
	srv := newTestServer(t, &fakeAuth{err: ErrNoSession}, countingRoutes(&executed)...)
	rec, _ := post(t, srv, `{"method":"demo.user"}`)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestHandlerErrorsKeepTheirCodeButInternalErrorsAreHidden(t *testing.T) {
	srv := newTestServer(t, &fakeAuth{user: "alice"},
		Handle("demo.invalid", func(context.Context, Caller, json.RawMessage) (any, error) {
			return nil, BadRequest("text must not be empty")
		}),
		Handle("demo.crash", func(context.Context, Caller, json.RawMessage) (any, error) {
			return nil, errors.New("disk path /secret/xyz failed")
		}),
		Handle("demo.panic", func(context.Context, Caller, json.RawMessage) (any, error) {
			panic("boom")
		}),
	)

	rec, env := post(t, srv, `{"method":"demo.invalid"}`)
	if rec.Code != http.StatusOK || env.Error.Code != "bad_request" || !strings.Contains(rec.Body.String(), "text must not be empty") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	for _, method := range []string{"demo.crash", "demo.panic"} {
		rec, env = post(t, srv, `{"method":"`+method+`"}`)
		if rec.Code != http.StatusOK || env.Error.Code != "internal" || strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "boom") {
			t.Fatalf("%s: got %d %s", method, rec.Code, rec.Body.String())
		}
	}
}

func TestRegistryRefusesUnusableRoutes(t *testing.T) {
	h := func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil }
	cases := map[string][]Route{
		"zero route (no policy)": {{}},
		"nil handler":            {Handle("demo.nil", nil)},
		"bad method name":        {Handle("Demo Method", h)},
		"duplicate method":       {Handle("demo.x", h), Handle("demo.x", h)},
	}
	for name, routes := range cases {
		if _, err := NewServerWithPermissions(&fakeAuth{}, []byte(emptyAppPolicy), routes...); err == nil {
			t.Errorf("%s: NewServerWithPermissions accepted it", name)
		}
	}
	if _, err := NewServerWithPermissions(nil, []byte(emptyAppPolicy), Handle("demo.x", h)); err == nil {
		t.Error("NewServerWithPermissions accepted a nil authenticator")
	}
}

func TestZeroCallerIsNotAuthenticated(t *testing.T) {
	var c Caller
	if c.Authenticated() || c.Admin() || c.User() != "" {
		t.Fatal("a Caller built outside the server must carry no authority")
	}
}
