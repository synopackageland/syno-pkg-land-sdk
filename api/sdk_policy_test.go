package api

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSDKPolicyCannotBeOverridden(t *testing.T) {
	h := func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil }
	for _, method := range []string{"nas.files.list", "bg.jobs.purge", "identity.login", "portal.register", "portal.unregister", "gateway.passthrough.status", "gateway.passthrough.set"} {
		for _, permission := range []string{"public", "authenticated", "administrators"} {
			raw := []byte(`{"schema":1,"default":"administrators","methods":{"` + method + `":"` + permission + `"}}`)
			if _, err := NewServerWithPermissions(&fakeAuth{}, raw, Handle(method, h)); err == nil {
				t.Fatalf("accepted override %s %s", method, permission)
			}
		}
	}
}
func TestSDKPolicyKeepsBothUserAndAdminBoundaries(t *testing.T) {
	h := func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil }
	s, err := NewServerWithPermissions(&fakeAuth{}, []byte(emptyAppPolicy), Handle("nas.files.list", h), Handle("bg.jobs.purge", h), Handle("identity.login", h), Handle("app.private", h), Handle("portal.status", h), Handle("portal.register", h), Handle("portal.unregister", h), Handle("bg.jobs.pause", h), Handle("bg.jobs.resume", h))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]policy{"nas.files.list": policyUser, "bg.jobs.purge": policyAdmin, "identity.login": policyPublic, "app.private": policyAdmin, "portal.status": policyUser, "portal.register": policyAdmin, "portal.unregister": policyAdmin, "bg.jobs.pause": policyUser, "bg.jobs.resume": policyUser} {
		if s.routes[name].policy != want {
			t.Fatal(name)
		}
	}
}
