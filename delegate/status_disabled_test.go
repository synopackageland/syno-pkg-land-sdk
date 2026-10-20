package delegate

import (
	"context"
	"encoding/json"
	"testing"
)

func TestStatusExposesOnlyVerifiedDisabledState(t *testing.T) {
	for _, tc := range []struct {
		wire, state string
		disabled    *bool
	}{
		{`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true,"disabled":false}`, "ready", boolRef(false)},
		{`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true,"disabled":true}`, "disabled", boolRef(true)},
		{`{"executor_digest":"e","policy_digest":"p","version":"1","ready":false,"disabled":false}`, "blocked", nil},
		{`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true}`, "blocked", nil},
	} {
		s := service(t, nil)
		s.Client = fakeExecutor(func(_ context.Context, op string, auth map[string]string, in, out any) error {
			if op != "status" || auth != nil {
				t.Fatal(op, auth)
			}
			return json.Unmarshal([]byte(tc.wire), out)
		})
		got := s.Status(context.Background(), nil)
		wire, _ := json.Marshal(got)
		var out struct {
			Disabled *bool `json:"disabled"`
		}
		json.Unmarshal(wire, &out)
		if got.State != tc.state || (out.Disabled == nil) != (tc.disabled == nil) || (out.Disabled != nil && *out.Disabled != *tc.disabled) {
			t.Fatal(string(wire), tc.state)
		}
	}
}
func boolRef(value bool) *bool { return &value }

func TestStatusDeclaresTrustedLongRunningPickerConfiguration(t *testing.T) {
	s := service(t, nil)
	s.Lifetime = "until-revoked"
	s.Destinations = []string{"save", "reports"}
	s.Client = fakeExecutor(func(_ context.Context, _ string, _ map[string]string, _ any, out any) error {
		return json.Unmarshal([]byte(`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true,"disabled":false}`), out)
	})
	out := rpc(t, s, "alice", "delegate.status", "{}")
	data := out["data"].(map[string]any)
	if data["lifetime"] != "until-revoked" || len(data["destinations"].([]any)) != 2 {
		t.Fatal(out)
	}
}

// The CGI forwards the caller's session so the executor judges the caller's
// Application Privileges; an App DSM refuses is never shown as ready.
func TestStatusOfACallerDSMRefusesIsBlocked(t *testing.T) {
	for _, tc := range []struct{ wire, state string }{
		{`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true,"disabled":false,"allowed":true}`, "ready"},
		{`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true,"disabled":false,"allowed":false}`, "blocked"},
	} {
		s := service(t, nil)
		s.Client = fakeExecutor(func(_ context.Context, op string, auth map[string]string, in, out any) error {
			if op != "status" || auth["dsm_cookie"] != "cookie-only" || auth["dsm_synotoken"] != "request-token" {
				t.Fatal(op, auth)
			}
			return json.Unmarshal([]byte(tc.wire), out)
		})
		data := rpc(t, s, "alice", "delegate.status", "{}")["data"].(map[string]any)
		if data["state"] != tc.state || (tc.state == "blocked" && data["reason"] != CodeAppDenied) {
			t.Fatal(data, tc.state)
		}
	}
}
