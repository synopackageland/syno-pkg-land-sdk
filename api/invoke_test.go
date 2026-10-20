package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInvokeRunsAJSONRouteForAVerifiedCaller(t *testing.T) {
	caller := Caller{identity: Identity{User: "alice"}, authenticated: true}
	r := httptest.NewRequest(http.MethodPost, "/api.cgi", nil)
	plain := Handle("a.plain", func(_ context.Context, c Caller, p json.RawMessage) (any, error) {
		return c.User() + string(p), nil
	})
	withRequest := HandleRequest("a.request", func(_ context.Context, c Caller, req *http.Request, p json.RawMessage) (any, error) {
		return req == r, nil
	})
	if out, err := plain.Invoke(context.Background(), caller, r, json.RawMessage(`{}`)); err != nil || out != "alice{}" {
		t.Fatalf("plain %v %v", out, err)
	}
	if out, err := withRequest.Invoke(context.Background(), caller, r, nil); err != nil || out != true {
		t.Fatalf("request %v %v", out, err)
	}
	stream := HandleStream("a.stream", func(context.Context, Caller, http.ResponseWriter, *http.Request) error { return nil })
	if _, err := stream.Invoke(context.Background(), caller, r, nil); err == nil {
		t.Fatal("invoked a stream route")
	}
}
