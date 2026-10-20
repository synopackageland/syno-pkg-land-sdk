package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONResourceLimitsBeforeAuthenticationAndDispatch(t *testing.T) {
	for name, params := range map[string]string{
		"wide": "[" + strings.Repeat("0,", 65536) + "0]",
		"deep": strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33),
	} {
		for _, access := range []string{"admin", "public"} {
			t.Run(name+"/"+access, func(t *testing.T) {
				executed := 0
				auth := &fakeAuth{user: "alice", admin: true}
				h := func(context.Context, Caller, json.RawMessage) (any, error) {
					executed++
					return nil, nil
				}
				route := Handle("demo.limit", h)
				if access == "public" {
					route = Handle("demo.limit", h)
				}
				srv, err := NewServerWithPermissions(auth, []byte(emptyAppPolicy), route)
				if access == "public" {
					srv, err = NewServerWithPermissions(auth, []byte(`{"schema":1,"default":"administrators","methods":{"demo.limit":"public"}}`), route)
				}
				if err != nil {
					t.Fatal(err)
				}
				_, env := post(t, srv, `{"method":"demo.limit","params":`+params+`}`)
				if env.Error == nil || env.Error.Code != "request_too_large" || executed != 0 || auth.calls != 0 {
					t.Fatalf("error=%+v executed=%d auth=%d", env.Error, executed, auth.calls)
				}
			})
		}
	}
}

func TestJSONResourceLimitsAllowLargeStringsAndPathSelections(t *testing.T) {
	for name, params := range map[string]any{
		"text":  strings.Repeat(`[]{}:,\"\\`, 10000),
		"paths": strings.Split(strings.Repeat("/volume1/share/file,", 999)+"/volume1/share/last", ","),
	} {
		t.Run(name, func(t *testing.T) {
			executed := 0
			srv := newTestServer(t, &fakeAuth{user: "alice", admin: true}, Handle("demo.limit", func(_ context.Context, c Caller, p json.RawMessage) (any, error) {
				executed++
				if !c.Admin() || !json.Valid(p) {
					t.Fatal("invalid caller or params")
				}
				return nil, nil
			}))
			body, err := json.Marshal(map[string]any{"method": "demo.limit", "params": params})
			if err != nil {
				t.Fatal(err)
			}
			_, env := post(t, srv, string(body))
			if !env.Success || executed != 1 {
				t.Fatalf("error=%+v executed=%d", env.Error, executed)
			}
		})
	}
}

func TestJSONParamsCannotOverrideAdminPolicy(t *testing.T) {
	for name, auth := range map[string]*fakeAuth{
		"anonymous":   {err: ErrNoSession},
		"normal user": {user: "bob"},
	} {
		t.Run(name, func(t *testing.T) {
			executed := 0
			srv := newTestServer(t, auth, countingRoutes(&executed)...)
			_, env := post(t, srv, `{"method":"demo.admin","params":{"admin":true,"user":"root","roles":["admin"],"scope":"admin"}}`)
			code := "admin_required"
			if auth.err != nil {
				code = "session_required"
			}
			if env.Error == nil || env.Error.Code != code || executed != 0 || auth.calls != 1 {
				t.Fatalf("error=%+v executed=%d auth=%d", env.Error, executed, auth.calls)
			}
		})
	}
}

func TestAppDeclaresRequestBodyLimitUpToTheCeiling(t *testing.T) {
	body := func(n int) string { return `{"method":"demo.user","params":"` + strings.Repeat("x", n) + `"}` }
	executed := 0
	srv := newTestServer(t, &fakeAuth{user: "alice"}, countingRoutes(&executed)...)
	if _, env := post(t, srv, body(MaxRequestBytes)); env.Error == nil || env.Error.Code != "request_too_large" {
		t.Fatalf("the default stays %d bytes: %+v", MaxRequestBytes, env.Error)
	}
	if err := srv.SetMaxRequestBytes(4 << 20); err != nil {
		t.Fatal(err)
	}
	if _, env := post(t, srv, body(3<<20)); !env.Success || executed != 1 {
		t.Fatalf("a declared 4 MiB limit admits 3 MiB: %+v", env.Error)
	}
	if _, env := post(t, srv, body(4<<20)); env.Error == nil || env.Error.Code != "request_too_large" || executed != 1 {
		t.Fatalf("the declared limit still refuses more: %+v", env.Error)
	}
	for _, n := range []int64{0, -1, MaxRequestBytesCeiling + 1} {
		if err := srv.SetMaxRequestBytes(n); err == nil {
			t.Fatalf("%d must be refused", n)
		}
	}
	if err := srv.SetMaxRequestBytes(MaxRequestBytesCeiling); err != nil {
		t.Fatal(err)
	}
}
