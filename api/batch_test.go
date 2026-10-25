package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestBatchAuthorizesEachMethodOncePerRequest(t *testing.T) {
	auth := &fakeAuth{user: "alice"}
	ran := 0
	h := func(context.Context, Caller, json.RawMessage) (any, error) { ran++; return "ok", nil }
	srv, err := NewServerWithPermissions(auth, []byte(`{"schema":1,"default":"administrators","methods":{"demo.read":"authenticated","demo.public":"public"}}`), Handle("demo.read", h), Handle("demo.public", h), Handle("demo.admin", h))
	if err != nil {
		t.Fatal(err)
	}
	_, env := post(t, srv, `{"v":1,"calls":[{"method":"demo.read"},{"method":"demo.admin"},{"method":"demo.unknown"},{"method":"demo.public"},{"method":"demo.read"}]}`)
	if !env.Success {
		t.Fatalf("batch rejected: %+v", env.Error)
	}
	b, _ := json.Marshal(env.Data)
	var out struct {
		Version int           `json:"v"`
		Results []BatchResult `json:"results"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Version != 1 || len(out.Results) != 5 || !out.Results[0].Success || out.Results[1].Error.Code != "admin_required" || out.Results[2].Error.Code != "unknown_method" || !out.Results[3].Success || !out.Results[4].Success || ran != 3 || auth.calls != 1 {
		t.Fatalf("out=%s ran=%d auth=%d", b, ran, auth.calls)
	}
	auth.err = ErrNoSession
	_, env = post(t, srv, `{"v":1,"calls":[{"method":"demo.read"},{"method":"demo.public"}]}`)
	if !env.Success || auth.calls != 2 {
		t.Fatalf("new request did not authenticate: %+v %d", env, auth.calls)
	}
}

func TestBatchRejectsInvalidEnvelopes(t *testing.T) {
	srv, _ := NewServerWithPermissions(&fakeAuth{user: "alice", admin: true}, []byte(emptyAppPolicy), Handle("demo.read", func(context.Context, Caller, json.RawMessage) (any, error) {
		t.Fatal("invalid envelope dispatched")
		return nil, nil
	}))
	for _, body := range []string{`{"v":2,"calls":[{"method":"demo.read"}]}`, `{"v":1,"calls":[]}`, `{"v":1,"method":"demo.read","calls":[{"method":"demo.read"}]}`, `{"v":1,"calls":[{"method":"demo.read","auth":{}}]}`, `{"method":"demo.read","Calls":[` + strings.Repeat(`{"method":"demo.read"},`, MaxBatchMethods) + `{"method":"demo.read"}]}`, `{"method":"demo.read","calls":[` + strings.Repeat(`{"method":"demo.read"},`, MaxBatchMethods) + `{"method":"demo.read"}]}}`, `{"method":"demo.read","V":2}`} {
		_, env := post(t, srv, body)
		if env.Success {
			t.Fatal(body)
		}
	}
}

func TestBatchIsolatesHandlerPanicsAndRefusesTransfers(t *testing.T) {
	ran := 0
	good := Handle("demo.good", func(context.Context, Caller, json.RawMessage) (any, error) { ran++; return nil, nil })
	srv, err := NewServerWithPermissions(&fakeAuth{user: "alice", admin: true}, []byte(emptyAppPolicy), good, Handle("demo.panic", func(context.Context, Caller, json.RawMessage) (any, error) { panic("private") }), HandleStream("demo.stream", func(context.Context, Caller, http.ResponseWriter, *http.Request) error {
		t.Fatal("stream ran")
		return nil
	}), HandleResponse("demo.cookie", func(context.Context, Caller, http.ResponseWriter, *http.Request, json.RawMessage) error {
		t.Fatal("cookie method ran")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, env := post(t, srv, `{"v":1,"calls":[{"method":"demo.good"},{"method":"demo.panic"},{"method":"demo.stream"},{"method":"demo.cookie"},{"method":"demo.good"}]}`)
	var out struct {
		Results []BatchResult `json:"results"`
	}
	json.Unmarshal(env.Data, &out)
	if len(out.Results) != 5 || !out.Results[0].Success || out.Results[1].Error.Code != "internal" || out.Results[2].Error.Code != "bad_request" || out.Results[3].Error.Code != "bad_request" || !out.Results[4].Success || ran != 2 {
		t.Fatalf("%s", env.Data)
	}
	calls := strings.TrimSuffix(strings.Repeat(`{"method":"demo.good"},`, 9), ",")
	_, env = post(t, srv, `{"v":1,"calls":[`+calls+`]}`)
	if env.Success || ran != 2 {
		t.Fatal("oversized batch ran")
	}
}

func TestBatchMissingParamsUsesEmptyObject(t *testing.T) {
	server, err := NewServerWithPermissions(&fakeAuth{user: "alice", admin: true}, []byte(emptyAppPolicy), Handle("demo.read", func(_ context.Context, _ Caller, params json.RawMessage) (any, error) {
		if string(params) != "{}" {
			t.Fatalf("missing batch params = %s", params)
		}
		return true, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, env := post(t, server, `{"v":1,"calls":[{"method":"demo.read"}]}`)
	if !env.Success {
		t.Fatal(env.Error)
	}
}
