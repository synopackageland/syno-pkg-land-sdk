package delegate

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func longRPC(t *testing.T, s *Service, user, method, raw, token string) map[string]any {
	t.Helper()
	methods := map[string]string{}
	// SDK routes use the shipped SDK roles, including the administrator floor.
	policy, _ := json.Marshal(map[string]any{"schema": 1, "default": "administrators", "methods": methods})
	server, err := api.NewServerWithPermissions(identity{user}, policy, s.Routes()...)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"method":"`+method+`","params":`+raw+`}`))
	req.Header.Set("Origin", "http://"+req.Host)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "id", Value: "fake-cookie"})
	req.Header.Set("X-SYNO-TOKEN", token)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	var out map[string]any
	if json.Unmarshal(recorder.Body.Bytes(), &out) != nil {
		t.Fatal(recorder.Body.String())
	}
	return out
}
func TestOperationRoutesForwardOwnerSessionAndRejectForgedIdentity(t *testing.T) {
	s := service(t, nil)
	calls := 0
	s.Client = fakeExecutor(func(_ context.Context, op string, auth map[string]string, in, out any) error {
		calls++
		if op != "files.operationStatus" && op != "files.cancelOperation" {
			t.Fatal(op)
		}
		if auth["dsm_synotoken"] != "alice-token" {
			return fail("DELEGATION_DENIED")
		}
		return json.Unmarshal([]byte(`{"job_id":"export-1","state":"canceled"}`), out)
	})
	for _, method := range []string{"delegate.operations.get", "delegate.operations.cancel"} {
		raw := `{"delegation_id":"grant","job_id":"export-1"}`
		if out := longRPC(t, s, "alice", method, raw, "alice-token"); out["success"] != true {
			t.Fatal(out)
		}
		if out := longRPC(t, s, "bob", method, raw, "bob-token"); out["success"] != false {
			t.Fatal("other owner admitted", out)
		}
		before := calls
		if out := longRPC(t, s, "alice", method, `{"delegation_id":"grant","job_id":"export-1","uid":0}`, "alice-token"); out["success"] != false || calls != before {
			t.Fatal("forged UID reached executor", out)
		}
		if out := longRPC(t, s, "alice", method, raw, ""); out["success"] != false || calls != before {
			t.Fatal("sessionless HTTP operation", out)
		}
	}
	for _, method := range []string{"delegate.disable", "delegate.enable"} {
		before := calls
		if out := longRPC(t, s, "alice", method, `{}`, "alice-token"); out["success"] != false || calls != before {
			t.Fatal("nonadmin management call", out)
		}
	}
}

func TestMoveRouteCannotReserveOtherOwnersMirror(t *testing.T) {
	s, in := stagedService(t)
	in.JobID += OwnerJobSuffix("alice", in.DelegationID)
	old := filepath.Join(s.StagingDir, in.Staging)
	in.Staging = OwnerStagingPrefix("alice", in.DelegationID) + "file"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(s.StagingDir, in.Staging)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old, filepath.Join(s.StagingDir, in.Staging)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(in)
	calls := 0
	s.Client = fakeExecutor(func(_ context.Context, op string, auth map[string]string, _ any, out any) error {
		switch op {
		case "delegate.list":
			if auth["dsm_synotoken"] == "bob-token" {
				return json.Unmarshal([]byte(`{"delegations":[]}`), out)
			}
			return json.Unmarshal([]byte(`{"delegations":[{"delegation_id":"grant","subject":{"username":"alice","uid":1205}}]}`), out)
		case "files.moveFromStaging":
			calls++
			raw, _ := json.Marshal(map[string]string{"job_id": in.JobID, "state": "queued"})
			return json.Unmarshal(raw, out)
		default:
			t.Fatal(op)
			return nil
		}
	})
	if out := longRPC(t, s, "bob", "delegate.move_from_staging", string(raw), "bob-token"); out["success"] != false {
		t.Fatal(out)
	}
	if bindings, err := s.Store.PendingMoves(context.Background()); err != nil || len(bindings) != 0 || calls != 0 {
		t.Fatal("other user reserved job", bindings, err, calls)
	}
	if out := longRPC(t, s, "alice", "delegate.move_from_staging", string(raw), "alice-token"); out["success"] != true || calls != 1 {
		t.Fatal(out, calls)
	}
}
