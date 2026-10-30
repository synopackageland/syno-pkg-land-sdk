package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestDefaultPermissionsRequireAdministratorGroup(t *testing.T) {
	for method, code := range map[string]string{"future.read": "admin_required", "future.unknown": "unknown_method"} {
		executed := 0
		auth := &fakeAuth{user: "admin"} // Name alone confers no group membership.
		srv, err := NewServerWithPermissions(auth, []byte(emptyAppPolicy), Handle("future.read", func(context.Context, Caller, json.RawMessage) (any, error) {
			executed++
			return nil, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		_, env := post(t, srv, `{"method":"`+method+`","params":{"admin":true,"groups":["administrators"]}}`)
		if env.Error == nil || env.Error.Code != code || executed != 0 || auth.calls != 1 {
			t.Fatalf("method=%s error=%+v executed=%d auth=%d", method, env.Error, executed, auth.calls)
		}
	}
}

func TestCentralPermissionsAreTheOnlyAccessDecision(t *testing.T) {
	called := 0
	h := func(context.Context, Caller, json.RawMessage) (any, error) { called++; return nil, nil }
	// A public constructor is unable to widen the default. The JSON explicitly
	// opens only these two methods; an admin constructor cannot override it.
	raw := []byte(`{"schema":1,"default":"administrators","methods":{"demo.user":"authenticated","demo.ping":"public"}}`)
	auth := &fakeAuth{user: "bob"}
	srv, err := NewServerWithPermissions(auth, raw, Handle("demo.user", h), Handle("demo.ping", h), Handle("demo.private", h))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, code string }{
		{"demo.user", ""}, {"demo.ping", ""}, {"demo.private", "admin_required"}, {"future.unknown", "unknown_method"},
	} {
		_, env := post(t, srv, `{"method":"`+tc.method+`"}`)
		if tc.code == "" {
			if !env.Success {
				t.Fatalf("%s: %+v", tc.method, env.Error)
			}
		} else if env.Error == nil || env.Error.Code != tc.code {
			t.Fatalf("%s: %+v", tc.method, env.Error)
		}
	}
	if called != 2 || auth.calls != 3 {
		t.Fatalf("called=%d auth=%d", called, auth.calls)
	}
	auth.err = ErrNoSession
	if _, env := post(t, srv, `{"method":"demo.user"}`); env.Error == nil || env.Error.Code != "session_required" {
		t.Fatal(env)
	}
}

func TestPermissionsRefuseUnknownMethods(t *testing.T) {
	s, err := NewServerWithPermissions(&fakeAuth{}, []byte(`{"schema":1,"default":"administrators","methods":{"typo.method":"authenticated"}}`))
	if err == nil || s != nil {
		t.Fatal("accepted unregistered permission")
	}
}

func TestAdministratorGroupMembershipDoesNotDependOnAccountName(t *testing.T) {
	auth := &fakeAuth{user: "alice", admin: true}
	s, err := NewServerWithPermissions(auth, []byte(emptyAppPolicy), Handle("demo.read", func(_ context.Context, c Caller, _ json.RawMessage) (any, error) { return c.User(), nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, env := post(t, s, `{"method":"demo.read"}`); !env.Success {
		t.Fatal(env)
	}
	auth.admin = false
	if _, env := post(t, s, `{"method":"demo.read"}`); env.Error == nil || env.Error.Code != "admin_required" {
		t.Fatal(env)
	}
}

func TestSDKPolicyDigestMismatchFailsClosed(t *testing.T) {
	before := expectedSDKPermissionsDigest
	t.Cleanup(func() { expectedSDKPermissionsDigest = before })
	h := func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil }
	expectedSDKPermissionsDigest = "mismatch"
	if err := ValidateAppMethods([]string{"demo.read"}); err == nil {
		t.Fatal("bridge inventory accepted mismatched SDK policy")
	}
	if srv, err := NewServerWithPermissions(&fakeAuth{}, []byte(emptyAppPolicy), Handle("demo.read", h)); err == nil || srv != nil {
		t.Fatal("SDK policy digest mismatch accepted")
	}
	expectedSDKPermissionsDigest = fmt.Sprintf("%x", sha256.Sum256(sdkPermissionsJSON))
	if err := ValidateAppMethods([]string{"demo.read"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServerWithPermissions(&fakeAuth{}, []byte(emptyAppPolicy), Handle("demo.read", h)); err != nil {
		t.Fatal(err)
	}
}

func TestPermissionParserSharedContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/permissions-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, JSON string
		Valid      bool
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			_, err := parsePermissions([]byte(tc.JSON))
			if (err == nil) != tc.Valid {
				t.Fatalf("valid=%v error=%v", tc.Valid, err)
			}
		})
	}
}
