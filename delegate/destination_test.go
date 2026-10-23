package delegate

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeExecutor func(context.Context, string, map[string]string, any, any) error

func (f fakeExecutor) Call(ctx context.Context, op string, auth map[string]string, in, out any) error {
	return f(ctx, op, auth, in, out)
}

type fakeSession struct {
	err        error
	calls      int
	user, path string
}

func (f *fakeSession) CheckPermission(_ context.Context, caller api.Caller, _ *http.Request, path string) error {
	f.calls++
	f.user, f.path = caller.User(), path
	return f.err
}
func destinationCaller(t *testing.T, user string) api.Caller {
	t.Helper()
	var caller api.Caller
	route := api.HandleRequest("test.caller", func(_ context.Context, c api.Caller, _ *http.Request, _ json.RawMessage) (any, error) {
		caller = c
		return struct{}{}, nil
	})
	srv, err := api.NewServerWithPermissions(identity{user}, []byte(`{"schema":1,"default":"administrators","methods":{"test.caller":"authenticated"}}`), route)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"method":"test.caller","params":{}}`))
	r.Header.Set("Origin", "http://"+r.Host)
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(httptest.NewRecorder(), r)
	return caller
}
func destinationRequest() *http.Request {
	r := httptest.NewRequest("POST", "/", nil)
	r.AddCookie(&http.Cookie{Name: "id", Value: "fake-cookie"})
	r.Header.Set("X-SYNO-TOKEN", "fake-token")
	return r
}
func TestSelectDestinationLeavesWritePermissionToExecutor(t *testing.T) {
	session := &fakeSession{}
	s := service(t, nil)
	s.SessionFiles = session
	s.Destinations = []string{"save"}
	s.Operations = []string{"files.moveFromStaging"}
	s.Lifetime = "until-revoked"
	calls := 0
	s.Client = fakeExecutor(func(_ context.Context, op string, auth map[string]string, in, out any) error {
		calls++
		if session.calls != 0 || op != "delegate.create" || auth["dsm_cookie"] != "fake-cookie" {
			t.Fatal("destination did not use executor authority")
		}
		data, _ := json.Marshal(in)
		if strings.Contains(string(data), "username") || strings.Contains(string(data), "uid") || strings.Contains(string(data), "job_id") || strings.Contains(string(data), "ttl_seconds") {
			t.Fatal("caller authority or legacy job leaked into grant")
		}
		return json.Unmarshal([]byte(`{"delegation_id":"grant","subject":{"username":"alice","uid":1205},"lifetime":"until-revoked","destination":"save","path":"/home"}`), out)
	})
	caller := destinationCaller(t, "alice")
	grant, err := s.SelectDestination(context.Background(), caller, destinationRequest(), SelectDestinationParams{Key: "save", Path: "/home"})
	if err != nil || grant.ID != "grant" || calls != 1 {
		t.Fatal(grant, err, calls)
	}
	s.Client = fakeExecutor(func(context.Context, string, map[string]string, any, any) error { return fail(CodePermissionDenied) })
	_, err = s.SelectDestination(context.Background(), caller, destinationRequest(), SelectDestinationParams{Key: "save", Path: "/home"})
	if code(t, err, CodePermissionDenied); calls != 1 {
		t.Fatal("failed write check saved grant")
	}
	session.err = nil
	for _, in := range []SelectDestinationParams{{Key: "other", Path: "/home"}, {Key: "save", Path: "/home/../etc"}, {Key: "save", Path: "/"}} {
		if _, err = s.SelectDestination(context.Background(), caller, destinationRequest(), in); err == nil || calls != 1 {
			t.Fatal("unbound destination saved grant")
		}
	}
	s.Lifetime = ""
	if _, err = s.SelectDestination(context.Background(), caller, destinationRequest(), SelectDestinationParams{Key: "save", Path: "/home"}); err == nil || calls != 1 {
		t.Fatal("legacy config silently made permanent")
	}
}
func TestSelectDestinationRejectsExecutorSubjectMismatch(t *testing.T) {
	for _, body := range []string{
		`{"delegation_id":"grant","subject":{"username":"bob","uid":1205},"lifetime":"until-revoked","destination":"save","path":"/home"}`,
		`{"delegation_id":"grant","subject":{"username":"alice","uid":1206},"lifetime":"until-revoked","destination":"save","path":"/home"}`,
		`{"delegation_id":"grant","subject":{"username":"alice","uid":1205},"lifetime":"until-revoked","destination":"other","path":"/home"}`,
		`{"delegation_id":"grant","subject":{"username":"alice","uid":1205},"expires_at":"2030-01-01T00:00:00Z"}`,
	} {
		t.Run(body, func(t *testing.T) {
			s := service(t, nil)
			s.SessionFiles = &fakeSession{}
			s.Destinations = []string{"save"}
			s.Operations = []string{"files.moveFromStaging"}
			s.Lifetime = "until-revoked"
			s.Client = fakeExecutor(func(_ context.Context, op string, _ map[string]string, in, out any) error {
				if op == "delegate.revoke" {
					return nil
				}
				return json.Unmarshal([]byte(body), out)
			})
			if _, err := s.SelectDestination(context.Background(), destinationCaller(t, "alice"), destinationRequest(), SelectDestinationParams{Key: "save", Path: "/home"}); err == nil {
				t.Fatal("unverified grant accepted")
			}
		})
	}
}
