package delegate

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mirrorRPC(t *testing.T, s *Service, user, params, token string) map[string]any {
	t.Helper()
	server, err := api.NewServerWithPermissions(contractIdentity(user), []byte(`{"schema":1,"default":"administrators","methods":{}}`), s.Routes()...)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"method":"delegate.operations.list","params":`+params+`}`))
	r.Header.Set("Origin", "http://"+r.Host)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cookie", "id="+user)
	r.Header.Set("X-SYNO-TOKEN", token)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	var out map[string]any
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal(w.Body.String())
	}
	return out
}
func TestOperationListUsesMirrorsAndFreshExactOwnerEvenForAdmin(t *testing.T) {
	s := service(t, nil)
	s.LookupUID = func(user string) string {
		switch user {
		case "alice":
			return "1205"
		case "bob":
			return "1206"
		case "admin":
			return "1207"
		}
		return ""
	}
	for _, key := range []string{"alice-job", "bob-job", "admin-job", "refused-job"} {
		id := strings.Split(key, "-")[0]
		if key == "refused-job" {
			id = "alice"
		}
		in := MoveParams{DelegationID: id, JobID: key, Staging: key, Name: key + ".csv", Size: 3}
		raw, _ := json.Marshal(in)
		if _, _, err := s.Store.ReserveMove(context.Background(), background.MoveBinding{JobID: key, DelegationID: id, Params: raw}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.FinishMoveMirror(context.Background(), "alice-job", true, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.FinishMoveMirror(context.Background(), "refused-job", false, "invalid-size"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.Client = fakeExecutor(func(_ context.Context, op string, auth map[string]string, in, out any) error {
		calls++
		if op != "delegate.list" || auth["dsm_synotoken"] != "valid" {
			t.Fatal(op, auth)
		}
		// Deliberately include all owners: the backend must still compare both names and UIDs.
		return json.Unmarshal([]byte(`{"delegations":[{"delegation_id":"alice","subject":{"username":"alice","uid":1205}},{"delegation_id":"bob","subject":{"username":"bob","uid":1206}},{"delegation_id":"admin","subject":{"username":"admin","uid":1207}}]}`), out)
	})
	for _, user := range []string{"alice", "bob", "admin"} {
		out := mirrorRPC(t, s, user, `{}`, "valid")
		if out["success"] != true {
			t.Fatal(out)
		}
		jobs := out["data"].(map[string]any)["operations"].([]any)
		want := 1
		if user == "alice" {
			want = 2
		}
		if len(jobs) != want {
			t.Fatal(user, jobs)
		}
		for _, raw := range jobs {
			job := raw.(map[string]any)
			if job["delegation_id"] != user || job["name"] == nil || job["state"] == nil || job["receipt"] != nil || job["source"] != nil || job["params"] != nil {
				t.Fatal(user, job)
			}
			if job["job_id"] == "alice-job" && job["state"] != "committed" {
				t.Fatal(job)
			}
			if job["job_id"] == "refused-job" && (job["state"] != "interrupted" || job["refusal"] != "invalid-size") {
				t.Fatal(job)
			}
		}
	}
	before := calls
	for _, bad := range []string{`{"uid":1205}`, `{"owner":"alice"}`, `{"limit":1}`, `null`} {
		if out := mirrorRPC(t, s, "alice", bad, "valid"); out["success"] != false || calls != before {
			t.Fatal("nonempty input reached executor", bad, out)
		}
	}
	if out := mirrorRPC(t, s, "alice", `{}`, ""); out["success"] != false || calls != before {
		t.Fatal("missing session", out)
	}
	// Current UID change cannot inherit even an identically named mirror/grant.
	s.LookupUID = func(string) string { return "9999" }
	if out := mirrorRPC(t, s, "alice", `{}`, "valid"); out["success"] != true || len(out["data"].(map[string]any)["operations"].([]any)) != 0 {
		t.Fatal(out)
	}
	s.Client = fakeExecutor(func(context.Context, string, map[string]string, any, any) error { return fail("AUTH_DENIED") })
	if out := mirrorRPC(t, s, "alice", `{}`, "valid"); out["success"] != false {
		t.Fatal("stale list fallback", out)
	}
}

// an admitted move is queued, not uncertain; only a send
// without an answer, or a mirror from before phases, is uncertain.
func TestOperationListIsUncertainOnlyWhenAdmissionIsUnknown(t *testing.T) {
	s := service(t, nil)
	ctx := context.Background()
	for _, key := range []string{"reserved", "accepted", "sent", "legacy"} {
		in := MoveParams{DelegationID: "alice", JobID: key, Staging: key, Name: key + ".csv", Size: 3}
		raw, _ := json.Marshal(in)
		phase := background.MoveReserved
		if key == "legacy" {
			phase = ""
		}
		if _, _, err := s.Store.ReserveMove(ctx, background.MoveBinding{JobID: key, DelegationID: "alice", Params: raw, Phase: phase}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.AcceptMove(ctx, "accepted"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Store.ClaimAdmission(ctx, "sent", time.Now(), 5, func(int) time.Duration { return time.Second }); !ok || err != nil {
		t.Fatal(ok, err)
	}
	s.Client = fakeExecutor(func(_ context.Context, _ string, _ map[string]string, _, out any) error {
		return json.Unmarshal([]byte(`{"delegations":[{"delegation_id":"alice","subject":{"username":"alice","uid":1205}}]}`), out)
	})
	want := map[string]string{"reserved": "queued", "accepted": "queued", "sent": "uncertain", "legacy": "uncertain"}
	for _, raw := range mirrorRPC(t, s, "alice", `{}`, "valid")["data"].(map[string]any)["operations"].([]any) {
		job := raw.(map[string]any)
		if job["state"] != want[job["job_id"].(string)] {
			t.Error(job)
		}
	}
}
