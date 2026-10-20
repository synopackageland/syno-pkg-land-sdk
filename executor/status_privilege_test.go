package main

import (
	"context"
	"encoding/json"
	"testing"
)

func statusAllowed(t *testing.T, r Response) (bool, bool) {
	t.Helper()
	if !r.OK {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r.Result)
	var out struct {
		Allowed *bool `json:"allowed"`
	}
	json.Unmarshal(b, &out)
	if out.Allowed == nil {
		return false, false
	}
	return *out.Allowed, true
}

// status with the caller's session answers whether DSM's Application
// Privileges let that caller use the App and File Station from its address,
// so a CGI never shows a ready App the executor would refuse (the regression test bug 4).
func TestStatusAnswersTheCallersAppPrivilege(t *testing.T) {
	e, f, _ := setup(t)
	allowOnly(f, browserAddr)
	if allowed, ok := statusAllowed(t, requestFrom(e, browserAddr, "status", map[string]any{})); !ok || !allowed {
		t.Fatal("allowed address", allowed, ok)
	}
	if allowed, ok := statusAllowed(t, requestFrom(e, "192.0.2.20", "status", map[string]any{})); !ok || allowed {
		t.Fatal("other address", allowed, ok)
	}
	if _, ok := statusAllowed(t, offline(e, "status", map[string]any{})); ok {
		t.Fatal("status without a session judged a caller")
	}
}

func TestStatusWithAnInvalidSessionIsAuthDenied(t *testing.T) {
	e, _, _ := setup(t)
	b, _ := json.Marshal(map[string]any{})
	if r := e.Handle(context.Background(), Request{Version: 1, Op: "status", Auth: Auth{RemoteAddr: browserAddr, hasKeys: true}, Params: b}); r.Code != "AUTH_DENIED" {
		t.Fatal(r)
	}
}
