package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/internal/identityroute"
)

type entryAuth struct {
	*fakeAuth
	mode string
}

func (a entryAuth) EntryMode() string { return a.mode }

func TestEntryFloorPrecedesEveryAppHandler(t *testing.T) {
	for _, mode := range []string{"admin-only", "app-privilege"} {
		for _, who := range []string{"anonymous", "denied", "admin"} {
			t.Run(mode+"/"+who, func(t *testing.T) {
				auth := &fakeAuth{user: "operator", admin: who == "admin"}
				code := ""
				if who == "anonymous" {
					auth.err, code = ErrNoSession, "session_required"
				} else if who == "denied" {
					auth.err, code = ErrAdminRequired, "admin_required"
					if mode == "app-privilege" {
						auth.err, code = ErrAppPrivilegeDenied, "app_privilege_denied"
					}
				}
				executed := 0
				h := func(_ context.Context, c Caller, _ json.RawMessage) (any, error) {
					executed++
					if !c.Authenticated() || !c.Admin() {
						t.Errorf("entry Caller lost authority: %+v", c)
					}
					return "ok", nil
				}
				group := &BatchGroup{Run: func(ctx context.Context, calls []AuthorizedCall) []BatchResult {
					results := []BatchResult{}
					for _, call := range calls {
						data, err := h(ctx, call.Caller, call.Params)
						if err != nil {
							t.Fatal(err)
						}
						results = append(results, BatchSuccess(data))
					}
					return results
				}}
				routes := []Route{
					Handle("app.json", h),
					HandleRequest("app.request", func(ctx context.Context, c Caller, _ *http.Request, p json.RawMessage) (any, error) {
						return h(ctx, c, p)
					}),
					HandleRequestBatch("app.group", func(ctx context.Context, c Caller, _ *http.Request, p json.RawMessage) (any, error) {
						return h(ctx, c, p)
					}, group),
					HandleResponse("app.response", func(ctx context.Context, c Caller, w http.ResponseWriter, _ *http.Request, p json.RawMessage) error {
						data, _ := h(ctx, c, p)
						WriteData(w, data)
						return nil
					}),
					HandleStream("app.transfer", func(ctx context.Context, c Caller, w http.ResponseWriter, _ *http.Request) error {
						data, _ := h(ctx, c, nil)
						WriteData(w, data)
						return nil
					}),
				}
				methods := map[string]string{}
				for _, route := range routes {
					methods[route.Method()] = "public"
				}
				raw, _ := json.Marshal(map[string]any{"schema": 1, "default": "administrators", "methods": methods})
				srv, err := NewServerWithPermissions(entryAuth{auth, mode}, raw, routes...)
				if err != nil {
					t.Fatal(err)
				}
				for _, body := range []string{
					`{"method":"app.json"}`, `{"method":"app.request"}`, `{"method":"app.response"}`,
					`{"v":1,"calls":[{"method":"app.json"},{"method":"app.group"},{"method":"app.json"}]}`,
					`{"v":1,"calls":[{"method":"app.group"},{"method":"unknown.method"}]}`,
				} {
					auth.calls = 0
					w, _ := post(t, srv, body)
					if code != "" && !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
						t.Fatalf("%s: %s", body, w.Body.String())
					}
					if auth.calls != 1 {
						t.Fatalf("%s authenticated %d times", body, auth.calls)
					}
				}
				for _, transport := range []string{"GET", "POST", "form"} {
					method, content, body := transport, "application/octet-stream", "upload"
					if transport == "form" {
						method, content, body = "POST", "application/x-www-form-urlencoded", "SynoToken=fixture"
					}
					r := httptest.NewRequest(method, "https://app.example/api.cgi?method=app.transfer", strings.NewReader(body))
					r.Header.Set("Content-Type", content)
					r.Header.Set("Origin", "https://app.example")
					w := httptest.NewRecorder()
					auth.calls = 0
					srv.ServeHTTP(w, r)
					if code != "" && !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
						t.Fatalf("%s: %s", transport, w.Body.String())
					}
					if auth.calls != 1 {
						t.Fatalf("%s authenticated %d times", transport, auth.calls)
					}
				}
				if code != "" && executed != 0 || code == "" && executed == 0 {
					t.Fatalf("executed=%d code=%s", executed, code)
				}
			})
		}
	}
}

func TestBuildBoundEntryCannotBeLostWithTheWrapper(t *testing.T) {
	oldEntry, oldDigest := expectedEntryMode, expectedPermissionsDigest
	t.Cleanup(func() { expectedEntryMode, expectedPermissionsDigest = oldEntry, oldDigest })
	raw := []byte(`{"schema":1,"default":"administrators","methods":{"app.public":"public"}}`)
	expectedEntryMode = "admin-only"
	expectedPermissionsDigest = fmt.Sprintf("%x", sha256.Sum256(raw))
	executed := 0
	route := Handle("app.public", func(context.Context, Caller, json.RawMessage) (any, error) {
		executed++
		return nil, nil
	})
	for _, auth := range []*fakeAuth{{err: ErrNoSession}, {user: "ordinary"}, {user: "operator", admin: true}} {
		srv, err := NewServerWithPermissions(auth, raw, route)
		if err != nil {
			t.Fatal(err)
		}
		_, env := post(t, srv, `{"method":"app.public"}`)
		if env.Success != auth.admin {
			t.Fatalf("admin=%v: %+v", auth.admin, env)
		}
	}
	if executed != 1 {
		t.Fatalf("executed=%d", executed)
	}
	if _, err := NewServerWithPermissions(&fakeAuth{}, append(raw, ' '), route); err == nil {
		t.Fatal("changed permissions accepted")
	}
	if _, err := NewServerWithPermissions(&fakeAuth{}, raw, route, Handle("app.unlisted", route.handler)); err == nil {
		t.Fatal("unlisted route accepted")
	}
	if _, err := DescribeRoutes(raw, route, Handle("app.unlisted", route.handler)); err == nil {
		t.Fatal("exported unlisted route")
	}
	expectedEntryMode = "app-privilege"
	if _, err := NewServerWithPermissions(&fakeAuth{}, raw, route); err == nil {
		t.Fatal("missing application privilege gate accepted")
	}
}

func TestPublicWithoutEntryKeepsAnonymousCallerAndNoAuthentication(t *testing.T) {
	auth := &fakeAuth{err: ErrNoSession}
	srv := newTestServer(t, auth, Handle("demo.ping", func(_ context.Context, c Caller, _ json.RawMessage) (any, error) {
		if c.Authenticated() || c.User() != "" {
			t.Fatal("public Caller changed")
		}
		return "ok", nil
	}))
	for _, body := range []string{`{"method":"demo.ping"}`, `{"v":1,"calls":[{"method":"demo.ping"},{"method":"demo.ping"}]}`} {
		_, env := post(t, srv, body)
		if !env.Success || auth.calls != 0 {
			t.Fatalf("%+v calls=%d", env, auth.calls)
		}
	}
}

func TestApplicationPrivilegePublicRouteKeepsAdmittedOrdinaryCaller(t *testing.T) {
	auth := entryAuth{&fakeAuth{user: "ordinary"}, "app-privilege"}
	calls := 0
	srv, err := NewServerWithPermissions(auth,
		[]byte(`{"schema":1,"default":"administrators","methods":{"app.public":"public"}}`),
		Handle("app.public", func(_ context.Context, c Caller, _ json.RawMessage) (any, error) {
			calls++
			if !c.Authenticated() || c.Admin() || c.User() != "ordinary" {
				t.Fatal("lost admitted ordinary caller")
			}
			return "ok", nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"method":"app.public"}`, `{"v":1,"calls":[{"method":"app.public"}]}`} {
		_, env := post(t, srv, body)
		if !env.Success {
			t.Fatalf("admitted ordinary caller denied: %+v", env)
		}
	}
	if calls != 2 {
		t.Fatalf("executed=%d", calls)
	}
}

func TestAppCannotImpersonateTheSDKIdentityCeremony(t *testing.T) {
	old := expectedEntryMode
	t.Cleanup(func() { expectedEntryMode = old })
	expectedEntryMode = "admin-only"
	for _, kind := range []string{"app", "invalid-key", "sdk"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			route := HandleResponse("identity.login", func(_ context.Context, _ Caller, w http.ResponseWriter, _ *http.Request, _ json.RawMessage) error {
				calls++
				WriteData(w, "ok")
				return nil
			})
			switch kind {
			case "invalid-key":
				route = SDKIdentityRoute(route, identityroute.Key{})
			case "sdk":
				route = SDKIdentityRoute(route, identityroute.New())
			}
			srv, err := NewServerWithPermissions(&fakeAuth{err: ErrNoSession}, []byte(emptyAppPolicy), route)
			if err != nil {
				t.Fatal(err)
			}
			w, _ := post(t, srv, `{"method":"identity.login"}`)
			if kind == "sdk" {
				if calls != 1 {
					t.Fatal("SDK ceremony unavailable")
				}
			} else if calls != 0 || !strings.Contains(w.Body.String(), `"code":"session_required"`) {
				t.Fatalf("App handler impersonated SDK ceremony: %d %s", calls, w.Body)
			}
		})
	}
}

func TestDescriptionAndDirectInvokeCannotDropBuildBoundEntry(t *testing.T) {
	old := expectedEntryMode
	t.Cleanup(func() { expectedEntryMode = old })
	expectedEntryMode = "admin-only"
	for _, admin := range []bool{false, true} {
		auth := &fakeAuth{user: "operator", admin: admin}
		h := WithDescription(http.NotFoundHandler(), auth, "missing-description.json")
		_, env := post(t, h, `{"method":"synopkgland.describe","params":{}}`, func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "id", Value: "fixture"})
			r.Header.Set("X-SYNO-TOKEN", "fixture")
		})
		want := "admin_required"
		if admin {
			want = "unknown_method"
		}
		if env.Error == nil || env.Error.Code != want {
			t.Fatalf("admin=%v: %+v", admin, env)
		}
	}
	executed := false
	route := Handle("app.public", func(context.Context, Caller, json.RawMessage) (any, error) {
		executed = true
		return nil, nil
	})
	for _, c := range []Caller{{}, {identity: Identity{User: "ordinary"}, authenticated: true}} {
		if _, err := route.Invoke(context.Background(), c, nil, nil); err == nil || executed {
			t.Fatal("direct invocation bypassed App entry")
		}
	}
}
