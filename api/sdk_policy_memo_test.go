package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func withSDKPermissions(t testing.TB, raw []byte) {
	t.Helper()
	old := sdkPermissionsJSON
	sdkPermissionsJSON = raw
	t.Cleanup(func() { sdkPermissionsJSON = old })
}

func manyRoutes(n int) []Route {
	h := func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil }
	routes := []Route{Handle("nas.files.list", h), Handle("bg.jobs.purge", h), Handle("identity.login", h)}
	for i := range n {
		routes = append(routes, Handle(fmt.Sprintf("app.route%d", i), h))
	}
	return routes
}

func manyRoutesPolicy(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"schema":1,"default":"administrators","methods":{`)
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"app.route%d":"authenticated"`, i)
	}
	b.WriteString("}}")
	return []byte(b.String())
}

func TestSDKPolicyDecodesOncePerProcessForManyRoutes(t *testing.T) {
	// A fresh copy has the same content at another address: still one decode.
	withSDKPermissions(t, append([]byte(nil), sdkPermissionsJSON...))
	before := sdkPolicyDecodes.Load()
	for range 3 {
		if _, err := NewServerWithPermissions(&fakeAuth{}, manyRoutesPolicy(200), manyRoutes(200)...); err != nil {
			t.Fatal(err)
		}
	}
	if got := sdkPolicyDecodes.Load() - before; got > 1 {
		t.Fatalf("SDK policy decoded %d times for 3 servers of 203 routes, want at most 1", got)
	}
}

func TestSDKPolicyMemoFollowsContent(t *testing.T) {
	withSDKPermissions(t, []byte(`{"files":{"nas.files.list":"authenticated"}}`))
	if rule, ok := sdkPolicy("nas.files.list"); !ok || rule != policyUser {
		t.Fatalf("got %v %v", rule, ok)
	}
	sdkPermissionsJSON = []byte(`{"files":{"nas.files.list":"administrators"}}`)
	if rule, ok := sdkPolicy("nas.files.list"); !ok || rule != policyAdmin {
		t.Fatalf("changed content not decoded again: %v %v", rule, ok)
	}
	if _, ok := sdkPolicy("bg.jobs.purge"); ok {
		t.Fatal("stale table")
	}
}

func panics(f func()) (v any) {
	defer func() { v = recover() }()
	f()
	return nil
}

func TestSDKPolicyKeepsItsPanics(t *testing.T) {
	withSDKPermissions(t, []byte(`{"files":`))
	for range 2 {
		if panics(func() { sdkPolicy("nas.files.list") }) == nil {
			t.Fatal("malformed SDK policy must panic on every lookup")
		}
	}
	// An invalid value panics only for its own method, as before.
	sdkPermissionsJSON = []byte(`{"files":{"nas.files.list":"everyone","nas.files.read":"authenticated"}}`)
	for range 2 {
		if v := panics(func() { sdkPolicy("nas.files.list") }); v != "invalid SDK policy" {
			t.Fatalf("panic %v", v)
		}
	}
	if rule, ok := sdkPolicy("nas.files.read"); !ok || rule != policyUser {
		t.Fatalf("got %v %v", rule, ok)
	}
	if _, ok := sdkPolicy("app.mine"); ok {
		t.Fatal("unknown method owned")
	}
}

func BenchmarkNewServerWithPermissions(b *testing.B) {
	raw, routes := manyRoutesPolicy(200), manyRoutes(200)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := NewServerWithPermissions(&fakeAuth{}, raw, routes...); err != nil {
			b.Fatal(err)
		}
	}
}
