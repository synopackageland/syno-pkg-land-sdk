package delegate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveRouteRejectsAnotherOwnersStagingWithOwnGrant(t *testing.T) {
	s, in := stagedService(t)
	// Bob has his own valid grant but guesses Alice's staging source.
	s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, _ any, out any) error {
		if op == "delegate.list" {
			return json.Unmarshal([]byte(`{"delegations":[{"delegation_id":"grant","subject":{"username":"bob","uid":1205}}]}`), out)
		}
		t.Fatal("cross-owner staging reached executor", op)
		return nil
	})
	in.JobID += OwnerJobSuffix("bob", in.DelegationID)
	in.Staging = "owners/" + strings.Repeat("a", 64) + "/result.txt"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(s.StagingDir, in.Staging)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.StagingDir, in.Staging), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(in)
	out := longRPC(t, s, "bob", "delegate.move_from_staging", string(raw), "bob-token")
	if out["success"] != false {
		t.Fatal("cross-owner staging accepted", out)
	}
}

func TestOwnerStagingPrefixSeparatesAccountsAndGrants(t *testing.T) {
	a := OwnerStagingPrefix("alice", "grant")
	if !validStaging(a+"file") || a == OwnerStagingPrefix("bob", "grant") || a == OwnerStagingPrefix("alice", "another") {
		t.Fatal("owner namespace collision")
	}
}

func TestMoveRouteCannotClaimAnUnscopedJobWithOwnedStaging(t *testing.T) {
	s, in := stagedService(t)
	s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, _ any, out any) error {
		if op == "delegate.list" {
			return json.Unmarshal([]byte(`{"delegations":[{"delegation_id":"grant","subject":{"username":"alice","uid":1205}}]}`), out)
		}
		t.Fatal("unscoped job reached executor", op)
		return nil
	})
	old := filepath.Join(s.StagingDir, in.Staging)
	in.Staging = OwnerStagingPrefix("alice", in.DelegationID) + "file"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(s.StagingDir, in.Staging)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old, filepath.Join(s.StagingDir, in.Staging)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(in)
	if out := longRPC(t, s, "alice", "delegate.move_from_staging", string(raw), "alice-token"); out["success"] != false {
		t.Fatal("unscoped global job admitted", out)
	}
}
