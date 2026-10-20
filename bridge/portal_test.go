//go:build unix

package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/portal"
)

func portalServer(t *testing.T, ex portal.Executor, admin bool) http.Handler {
	t.Helper()
	sv := services{portal: func() portal.Status { return portal.Status{State: portal.Inactive} }, portalExecutor: ex}
	h, err := api.NewServerWithPermissions(userAuth{api.Identity{User: "alice", Admin: admin}}, []byte(`{"schema":1,"default":"administrators","methods":{}}`), sv.routes()...)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPortalBrowserRoutesForwardOnlyTheAdminSession(t *testing.T) {
	for _, op := range []string{"portal.register", "portal.unregister"} {
		ex := &portalRecorder{fakeExecutor: fakeExecutor{result: `{"entry":"registered","portal":"registered"}`}}
		h := portalServer(t, ex, true)
		r := httptest.NewRequest("POST", "https://nas.local:5001/api.cgi", strings.NewReader(`{"method":"`+op+`","params":{}}`))
		r.RemoteAddr = "192.0.2.9:1234"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://nas.local:5001")
		for k, v := range dsmHeaders {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if !strings.Contains(w.Body.String(), `"success":true`) || !strings.Contains(w.Body.String(), `"entry":"registered"`) {
			t.Fatal(op, w.Body.String())
		}
		if ex.op != "status" || ex.auth != nil {
			t.Fatal("status leaked credentials", ex.op)
		}
		if ex.changedOp != op || ex.changedAuth["dsm_cookie"] != "sid1" || ex.changedAuth["dsm_synotoken"] != "tok1" || ex.changedAuth["remote_addr"] != "192.0.2.9" || ex.changedParams != "{}" {
			t.Fatal("wrong mutation carrier", ex.changedOp, ex.changedAuth, ex.changedParams)
		}
	}
}

func TestPortalChangeRefusalsDoNotReachTheExecutor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		admin   bool
		params  string
		headers map[string]string
		code    string
	}{
		{"nonadmin", false, `{}`, dsmHeaders, "admin_required"},
		{"params", true, `{"package":"another"}`, dsmHeaders, "bad_request"},
		{"no session", true, `{}`, nil, "session_required"},
		{"no token", true, `{}`, map[string]string{"Cookie": "id=sid1"}, "credential_not_ready"},
	} {
		ex := &fakeExecutor{}
		got := serve(t, portalServer(t, ex, tc.admin), `{"method":"portal.register","params":`+tc.params+`}`, tc.headers)
		if !strings.Contains(got, `"code":"`+tc.code+`"`) || ex.op != "" {
			t.Fatal(tc.name, got, ex.op)
		}
	}
}

func TestPortalStatusReportsTheExecutorEntry(t *testing.T) {
	for _, entry := range []string{portal.EntryRegistered, portal.EntryUnregistered, portal.EntryCleanupRequired} {
		ex := &fakeExecutor{result: `{"portal":"` + entry + `"}`}
		got := serve(t, portalServer(t, ex, true), `{"method":"portal.status"}`, dsmHeaders)
		if !strings.Contains(got, `"entry":"`+entry+`"`) {
			t.Fatal(got)
		}
	}
	ex := &fakeExecutor{err: &api.Error{Code: "EXECUTOR_UNAVAILABLE", Message: "unavailable"}}
	got := serve(t, portalServer(t, ex, true), `{"method":"portal.status"}`, dsmHeaders)
	if !strings.Contains(got, `"state":"unavailable"`) {
		t.Fatal(got)
	}
}

func TestPortalExecutorServicesAreOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		sv := Config{PackageID: "hello", DataDir: t.TempDir(), PortalExecutor: enabled}.services(Manifest{Capabilities: Capabilities{Portal: true, Root: true}})
		names := map[string]bool{}
		for _, r := range sv.routes() {
			names[r.Method()] = true
		}
		if names["portal.register"] != enabled || names["portal.unregister"] != enabled {
			t.Fatal(enabled, names)
		}
	}
}

// Mutations keep the verified cookie, token and original address; params cannot choose a package.
func TestPortalChangeUsesTheRequestCarrier(t *testing.T) {
	ex := &fakeExecutor{result: `{"entry":"registered"}`}
	sv := services{portal: func() portal.Status { return portal.Status{State: portal.Inactive} }, portalExecutor: ex}
	r := httptest.NewRequest("POST", "/api.cgi", nil)
	r.RemoteAddr = "192.0.2.9:1234"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://nas.local:5001")
	for k, v := range dsmHeaders {
		r.Header.Set(k, v)
	}
	_, err := sv.portalChange(portal.Register)(context.Background(), api.Caller{}, r, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("anonymous caller")
	}
}

// Record the mutation separately from the fresh status read following it.
type portalRecorder struct {
	fakeExecutor
	changedOp     string
	changedAuth   map[string]string
	changedParams string
}

func (r *portalRecorder) Call(ctx context.Context, op string, auth map[string]string, params, result any) error {
	if op != "status" {
		r.changedOp = op
		r.changedAuth = auth
		b, _ := json.Marshal(params)
		r.changedParams = string(b)
	}
	return r.fakeExecutor.Call(ctx, op, auth, params, result)
}

func TestPortalChangeKeepsExecutorRefusalCodes(t *testing.T) {
	for _, code := range []string{"PORTAL_FAILED", "POLICY_DENIED", "ADMIN_REQUIRED"} {
		ex := &fakeExecutor{err: &api.Error{Code: code, Message: "refused"}}
		got := serve(t, portalServer(t, ex, true), `{"method":"portal.register","params":{}}`, dsmHeaders)
		if !strings.Contains(got, `"code":"`+code+`"`) {
			t.Fatal(got)
		}
	}
}
