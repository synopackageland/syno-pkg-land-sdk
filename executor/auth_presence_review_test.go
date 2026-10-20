package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReviewEmptyPartialAuthMustNotBecomeWorkerRevoke(t *testing.T) {
	for _, auth := range []string{`{"dsm_cookie":""}`, `{"dsm_synotoken":""}`, `{"dsm_cookie":"","dsm_synotoken":""}`, `null`} {
		e, _, _ := setup(t)
		id := create(t, e)
		var req Request
		p, _ := json.Marshal(map[string]string{"delegation_id": id, "job_id": "job1"})
		raw := []byte(`{"v":1,"op":"delegate.revoke","auth":` + auth + `,"params":` + string(p) + `}`)
		if err := strictJSON(raw, &req); err != nil {
			t.Fatal(err)
		}
		if r := e.Handle(context.Background(), req); r.Code != "AUTH_DENIED" {
			t.Errorf("partial auth %s became worker revoke: %+v", auth, r)
		}
	}
}

func TestWorkerRevokeRequiresAbsentOrEmptyAuthObject(t *testing.T) {
	for _, auth := range []string{"", `,"auth":{}`} {
		e, _, _ := setup(t)
		id := create(t, e)
		p, _ := json.Marshal(map[string]string{"delegation_id": id, "job_id": "job1"})
		var req Request
		if err := strictJSON([]byte(`{"v":1,"op":"delegate.revoke"`+auth+`,"params":`+string(p)+`}`), &req); err != nil {
			t.Fatal(err)
		}
		if r := e.Handle(context.Background(), req); !r.OK {
			t.Fatal("valid worker revoke denied", r)
		}
	}
}
