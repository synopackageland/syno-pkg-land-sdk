package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSelectDestinationUsesOnlyExecutorPermissionAuthority(t *testing.T) {
	s := service(t, nil)
	legacy := &fakeSession{err: errors.New("browser session must never reach a relay")}
	s.SessionFiles = legacy
	s.Destinations, s.Operations, s.Lifetime = []string{"save"}, []string{"files.moveFromStaging"}, "until-revoked"
	s.Client = fakeExecutor(func(_ context.Context, op string, auth map[string]string, in, out any) error {
		if op != "delegate.create" || auth["remote_addr"] == "" {
			t.Fatalf("missing verified request carriers: %s %v", op, auth)
		}
		return json.Unmarshal([]byte(`{"delegation_id":"grant","subject":{"username":"alice","uid":1205},"lifetime":"until-revoked","destination":"save","path":"/home"}`), out)
	})
	if _, err := s.SelectDestination(context.Background(), destinationCaller(t, "alice"), destinationRequest(), SelectDestinationParams{Key: "save", Path: "/home"}); err != nil {
		t.Fatal(err)
	}
	if legacy.calls != 0 {
		t.Fatal("legacy session preflight ran")
	}
	s.SessionFiles = nil
	if _, err := s.SelectDestination(context.Background(), destinationCaller(t, "alice"), destinationRequest(), SelectDestinationParams{Key: "save", Path: "/home"}); err != nil {
		t.Fatal("executor-only selection unavailable", err)
	}
}
