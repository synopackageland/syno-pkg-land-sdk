package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// svcReply runs one svc through a Session and returns the svc_result frame
// the App received.
func svcReply(t *testing.T, services map[string]Service, op string, params any) map[string]any {
	t.Helper()
	var got map[string]any
	app := fakeApp{methods: []string{"a.b"}, handle: func(c *fakeConn, _ map[string]any) map[string]any {
		got = c.svc(op, params)
		return map[string]any{"ok": true}
	}}
	s := &Session{Dial: pipeDialer(app), Methods: []string{"a.b"}, Services: services}
	if _, err := s.Call(context.Background(), "a.b", nil, &Call{Context: alice}); err != nil {
		t.Fatal(err)
	}
	return got
}

func svcError(reply map[string]any) string {
	e, _ := reply["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestServiceCodesFitTheFrameSchema(t *testing.T) {
	services := map[string]Service{"x.y": func(context.Context, *Call, json.RawMessage) (any, error) {
		return nil, &api.Error{Code: "invalid-request", Message: "no"}
	}}
	if code := svcError(svcReply(t, services, "x.y", nil)); code != "invalid_request" {
		t.Fatalf("code %q", code)
	}
}

func TestServiceParamsAreBounded(t *testing.T) {
	ran := false
	services := map[string]Service{"x.y": func(context.Context, *Call, json.RawMessage) (any, error) {
		ran = true
		return nil, nil
	}}
	big := map[string]string{"s": strings.Repeat("a", MaxServiceParams)}
	if code := svcError(svcReply(t, services, "x.y", big)); code != "request_too_large" || ran {
		t.Fatalf("code %q ran %v", code, ran)
	}
}

func TestAPanickingServiceIsInternal(t *testing.T) {
	services := map[string]Service{"x.y": func(context.Context, *Call, json.RawMessage) (any, error) {
		panic("boom")
	}}
	if code := svcError(svcReply(t, services, "x.y", nil)); code != "internal" {
		t.Fatalf("code %q", code)
	}
}

// root.run and root.start carry program params as large as the executor's
// 1 MiB request; every other service stays at 64 KiB.
func TestRootProgramParamsMayReachAMebibyte(t *testing.T) {
	for _, op := range []string{"root.run", "root.start"} {
		ran := false
		services := map[string]Service{op: func(context.Context, *Call, json.RawMessage) (any, error) {
			ran = true
			return map[string]bool{"ok": true}, nil
		}}
		big := map[string]string{"s": strings.Repeat("a", 512<<10)}
		if reply := svcReply(t, services, op, big); reply["ok"] != true || !ran {
			t.Fatal(op, reply)
		}
		ran = false
		huge := map[string]string{"s": strings.Repeat("a", MaxRootServiceParams)}
		if code := svcError(svcReply(t, services, op, huge)); code != "request_too_large" || ran {
			t.Fatalf("%s code %q ran %v", op, code, ran)
		}
	}
	if MaxRootServiceParams != 1<<20 || serviceParamsLimit("root.jobs.get") != MaxServiceParams {
		t.Fatal("limits")
	}
}
