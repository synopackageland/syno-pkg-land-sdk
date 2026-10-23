package delegate

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCheckDestinationRechecksOnlyTheCurrentOwnersExactGrant(t *testing.T) {
	s := service(t, nil)
	s.Scope, s.Lifetime, s.Destinations = "home", "until-revoked", []string{"save"}
	checks := 0
	allowed := true
	s.Client = fakeExecutor(func(_ context.Context, op string, auth map[string]string, in, out any) error {
		if auth["remote_addr"] == "" || auth["dsm_cookie"] != "fake-cookie" || auth["dsm_synotoken"] != "fake-token" {
			t.Fatal("original carriers lost")
		}
		switch op {
		case "status":
			return json.Unmarshal([]byte(`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true,"disabled":false,"allowed":true,"selection":{"scope":"home","root":"/home","generation":0}}`), out)
		case "delegate.list":
			return json.Unmarshal([]byte(`{"delegations":[{"delegation_id":"current","destination":"save","scope":"home","scope_generation":0,"path":"/home","subject":{"username":"alice","uid":1205},"lifetime":"until-revoked"}]}`), out)
		case "files.checkPermission":
			checks++
			b, _ := json.Marshal(in)
			if string(b) != `{"delegation_id":"current","path":"/home"}` {
				t.Fatal(string(b))
			}
			b, _ = json.Marshal(map[string]bool{"allowed": allowed})
			return json.Unmarshal(b, out)
		default:
			t.Fatal(op)
			return nil
		}
	})
	caller, r := destinationCaller(t, "alice"), destinationRequest()
	if err := s.CheckDestination(context.Background(), caller, r, "save", "/home"); err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatal("write permission was not rechecked")
	}
	allowed = false
	if err := s.CheckDestination(context.Background(), caller, r, "save", "/home"); errorCode(err) != CodePermissionDenied {
		t.Fatal(err)
	}
	for _, path := range []string{"/home/child", "/other"} {
		if err := s.CheckDestination(context.Background(), caller, r, "save", path); err == nil {
			t.Fatal("widened path accepted")
		}
	}
	if err := s.CheckDestination(context.Background(), destinationCaller(t, "bob"), r, "save", "/home"); err == nil {
		t.Fatal("other owner accepted")
	}
	if checks != 2 {
		t.Fatal("unbound permission check dispatched", checks)
	}
}
