package delegate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDestinationUsesProtectedSelectionAndThisOwnerOnly(t *testing.T) {
	s := service(t, nil)
	s.Scope = "install-selected"
	s.Lifetime = "until-revoked"
	s.Destinations = []string{"save"}
	s.InstallDraftPath = filepath.Join(t.TempDir(), "delegate-install.json")
	os.WriteFile(s.InstallDraftPath, []byte(`{"schema":1,"scope":"shared:untrusted"}`), 0600)
	grants := `[{"delegation_id":"old","destination":"save","scope":"shared:reports","scope_generation":1,"path":"/reports/old","subject":{"username":"alice","uid":1205},"lifetime":"until-revoked"},{"delegation_id":"other","destination":"save","scope":"shared:reports","scope_generation":2,"path":"/reports/other","subject":{"username":"bob","uid":1206},"lifetime":"until-revoked"},{"delegation_id":"current","destination":"save","scope":"shared:reports","scope_generation":2,"path":"/reports/export","subject":{"username":"alice","uid":1205},"lifetime":"until-revoked"}]`
	s.Client = fakeExecutor(func(_ context.Context, op string, auth map[string]string, _ any, out any) error {
		if op == "status" {
			return json.Unmarshal([]byte(`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true,"disabled":false,"allowed":true,"scope_state":"","selection":{"scope":"shared:reports","root":"/reports","generation":2}}`), out)
		}
		if op != "delegate.list" || auth["dsm_cookie"] != "fake-cookie" {
			t.Fatal(op, auth)
		}
		return json.Unmarshal([]byte(`{"delegations":`+grants+`}`), out)
	})
	status := s.Status(context.Background(), nil)
	if !status.Mismatch || status.Preselection != "shared:untrusted" || status.Selection.Scope != "shared:reports" {
		t.Fatal(status)
	}
	got, err := s.Destination(context.Background(), destinationCaller(t, "alice"), destinationRequest(), "save")
	if err != nil || got.Status != "active" || got.Destination.ID != "current" || got.Selection.Scope != "shared:reports" {
		t.Fatal(got, err)
	}
	grants = `[{"delegation_id":"old","destination":"save","scope":"home","scope_generation":1,"path":"/home","subject":{"username":"alice","uid":1205},"lifetime":"until-revoked"}]`
	got, err = s.Destination(context.Background(), destinationCaller(t, "alice"), destinationRequest(), "save")
	if err != nil || got.Status != "reselection-required" || got.Destination != nil {
		t.Fatal(got, err)
	}
	grants = `[{"delegation_id":"current","destination":"save","scope":"shared:reports","scope_generation":2,"path":"/reports/export","subject":{"username":"alice","uid":1205},"lifetime":"until-revoked","revoked":true}]`
	got, err = s.Destination(context.Background(), destinationCaller(t, "alice"), destinationRequest(), "save")
	if err != nil || got.Status != "revoked" || got.Destination != nil {
		t.Fatal(got, err)
	}
	grants = `[{"delegation_id":"current","destination":"save","scope":"shared:reports","scope_generation":2,"path":"/reports/export","subject":{"username":"alice","uid":1205},"lifetime":"until-revoked","revoked":true},{"delegation_id":"old","destination":"save","scope":"home","scope_generation":1,"path":"/home","subject":{"username":"alice","uid":1205},"lifetime":"until-revoked"}]`
	got, err = s.Destination(context.Background(), destinationCaller(t, "alice"), destinationRequest(), "save")
	if err != nil || got.Status != "reselection-required" {
		t.Fatal(got, err)
	}
	if _, err = s.Destination(context.Background(), destinationCaller(t, "alice"), destinationRequest(), "unknown"); err == nil {
		t.Fatal("unknown key allowed")
	}
}
func TestSelectedScopeMissingAndMalformedEvidenceNeverBecomeAuthority(t *testing.T) {
	for _, wire := range []string{
		`{"scope":"shared:reports","root":"/wrong","generation":1}`,
		`{"scope":"shared:reports","root":"/reports","generation":0}`,
		`{"scope":"shared:reports","root":"/reports","generation":9007199254740992}`,
	} {
		s := service(t, nil)
		s.Scope = "install-selected"
		s.Client = fakeExecutor(func(_ context.Context, _ string, _ map[string]string, _ any, out any) error {
			return json.Unmarshal([]byte(`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true,"disabled":false,"selection":`+wire+`}`), out)
		})
		if got := s.Status(context.Background(), nil); got.State != "blocked" || got.Selection != nil {
			t.Fatal(got)
		}
	}
}

// A caller DSM refuses the App learns that, not that the executor is down.
func TestDestinationOfACallerDSMRefusesIsAppDenied(t *testing.T) {
	s := service(t, nil)
	s.Lifetime = "until-revoked"
	s.Destinations = []string{"save"}
	s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, _ any, out any) error {
		if op != "status" {
			t.Fatal(op)
		}
		return json.Unmarshal([]byte(`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true,"disabled":false,"allowed":false}`), out)
	})
	if _, err := s.Destination(context.Background(), destinationCaller(t, "alice"), destinationRequest(), "save"); errorCode(err) != CodeAppDenied {
		t.Fatal(err)
	}
}
