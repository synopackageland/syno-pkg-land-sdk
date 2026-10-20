package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestEntryRefusalsReachTheBrowserAsStableCodes(t *testing.T) {
	raw := []byte(`{"schema":1,"default":"administrators","methods":{"demo.user":"authenticated"}}`)
	for _, tc := range []struct {
		err  error
		code string
	}{
		{ErrAdminRequired, "admin_required"},
		{ErrAppPrivilegeDenied, "app_privilege_denied"},
		{errors.New("bridge exited 2"), "auth_unavailable"},
	} {
		executed := 0
		srv, err := NewServerWithPermissions(&fakeAuth{err: tc.err}, raw, Handle("demo.user", func(context.Context, Caller, json.RawMessage) (any, error) {
			executed++
			return nil, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"demo.user", "future.unknown"} {
			_, env := post(t, srv, `{"method":"`+method+`"}`)
			if env.Error == nil || env.Error.Code != tc.code || executed != 0 {
				t.Fatalf("%v %s: %+v executed=%d", tc.err, method, env.Error, executed)
			}
		}
	}
}

func TestRequireAdminLetsHandlersReserveGlobalSettings(t *testing.T) {
	if err := RequireAdmin(Caller{identity: Identity{User: "alice", Admin: true}, authenticated: true}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Caller{{identity: Identity{User: "bob"}, authenticated: true}, {identity: Identity{User: "alice", Admin: true}}, {}} {
		var apiErr *Error
		if err := RequireAdmin(c); !errors.As(err, &apiErr) || apiErr.Code != "admin_required" {
			t.Fatalf("%+v: %v", c, err)
		}
	}
}
