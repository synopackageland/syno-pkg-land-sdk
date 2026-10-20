package delegate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportExampleProducesPrivateStagingAndCallsCompletionOnce(t *testing.T) {
	s := service(t, nil)
	s.Scope = "home"
	s.Lifetime = "until-revoked"
	s.Operations = []string{"files.moveFromStaging"}
	s.Destinations = []string{"save"}
	if err := os.MkdirAll(s.StagingDir, 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, params any, out any) error {
		switch op {
		case "status":
			return json.Unmarshal([]byte(`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true,"disabled":false,"allowed":true}`), out)
		case "delegate.list":
			return json.Unmarshal([]byte(`{"delegations":[{"delegation_id":"grant","subject":{"username":"alice","uid":1205},"scope":"home","path":"/home/export","destination":"save","lifetime":"until-revoked"}]}`), out)
		case "files.moveFromStaging":
			calls++
			in := params.(MoveParams)
			b, err := os.ReadFile(filepath.Join(s.StagingDir, in.Staging))
			if err != nil || !strings.Contains(string(b), "sdk-example") {
				t.Fatal(string(b), err)
			}
			info, _ := os.Stat(filepath.Join(s.StagingDir, in.Staging))
			if info.Mode().Perm() != 0600 {
				t.Fatal(info.Mode())
			}
			return json.Unmarshal([]byte(`{"job_id":"example-1","state":"queued"}`), out)
		case "files.operationStatus":
			return json.Unmarshal([]byte(`{"job_id":"example-1","state":"queued"}`), out)
		}
		t.Fatal(op)
		return nil
	})
	for i := 0; i < 2; i++ {
		ref, err := s.ExportExample(context.Background(), destinationCaller(t, "alice"), destinationRequest(), "save", "example-1")
		if err != nil || ref.JobID != "example-1" || ref.DelegationID != "grant" {
			t.Fatal(ref, err)
		}
	}
	if calls != 1 {
		t.Fatal("replayed admission", calls)
	}
	if _, err := s.ExportExample(context.Background(), destinationCaller(t, "alice"), destinationRequest(), "save", "../escape"); err == nil {
		t.Fatal("unsafe job id")
	}
}

func TestExportExampleRecoversAnUnsentAdmission(t *testing.T) {
	s := service(t, nil)
	s.Scope, s.Lifetime, s.Operations, s.Destinations = "home", "until-revoked", []string{"files.moveFromStaging"}, []string{"save"}
	if err := os.MkdirAll(s.StagingDir, 0700); err != nil {
		t.Fatal(err)
	}
	e := newDedupExecutor()
	offline := true
	s.Client = fakeExecutor(func(ctx context.Context, op string, auth map[string]string, params, out any) error {
		switch op {
		case "status":
			return json.Unmarshal([]byte(`{"executor_digest":"e","policy_digest":"p","version":"1","ready":true,"disabled":false,"allowed":true}`), out)
		case "delegate.list":
			return json.Unmarshal([]byte(`{"delegations":[{"delegation_id":"grant","subject":{"username":"alice","uid":1205},"scope":"home","path":"/home/export","destination":"save","lifetime":"until-revoked"}]}`), out)
		}
		if offline {
			return &dialFailure{fail("EXECUTOR_UNAVAILABLE")}
		}
		return e.Call(ctx, op, auth, params, out)
	})
	_, err := s.ExportExample(context.Background(), destinationCaller(t, "alice"), destinationRequest(), "save", "example-1")
	code(t, err, "EXECUTOR_UNAVAILABLE")
	offline = false
	for i := 0; i < 2; i++ {
		if _, err = s.ExportExample(context.Background(), destinationCaller(t, "alice"), destinationRequest(), "save", "example-1"); err != nil {
			t.Fatal(err)
		}
	}
	if moves, created := e.counts(); moves != 1 || created != 1 {
		t.Fatal(moves, created)
	}
}
