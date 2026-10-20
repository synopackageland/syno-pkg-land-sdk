//go:build unix

package bridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"github.com/synopackageland/syno-pkg-land-sdk/delegate"
	"github.com/synopackageland/syno-pkg-land-sdk/filestation"
	"github.com/synopackageland/syno-pkg-land-sdk/portal"
)

// dsmHeaders are a browser request's DSM session.
var dsmHeaders = map[string]string{"Cookie": "id=sid1", "X-SYNO-TOKEN": "tok1"}

// svcAs runs op as the App would during user's call of a.b, through the
// runtime's routes (a public method when user is empty), and returns the svc_result frame. A spooled file the
// service returned is read during the call, before the spool is removed.
func svcAs(t *testing.T, sv services, user string, op string, params any) (reply map[string]any, spooled string) {
	t.Helper()
	app := fakeApp{methods: []string{"a.b"}, handle: func(c *fakeConn, _ map[string]any) map[string]any {
		reply = c.svc(op, params)
		if r, ok := reply["result"].(map[string]any); ok {
			if path, ok := r["path"].(string); ok {
				b, _ := os.ReadFile(path)
				spooled = string(b)
			}
		}
		return map[string]any{"ok": true}
	}}
	session := &Session{Dial: pipeDialer(app), Methods: []string{"a.b"}, Services: sv.table(), Spool: filepath.Join(t.TempDir(), SpoolDir)}
	level := "authenticated"
	if user == "" {
		level = "public"
	}
	srv, err := api.NewServerWithPermissions(userAuth{api.Identity{User: user}}, []byte(`{"schema":1,"default":"administrators","methods":{"a.b":"`+level+`"}}`), Routes(session)...)
	if err != nil {
		t.Fatal(err)
	}
	if got := serve(t, srv, `{"method":"a.b"}`, dsmHeaders); !strings.Contains(got, `"success":true`) {
		t.Fatalf("call: %s", got)
	}
	return reply, spooled
}

type fakeExecutor struct {
	op     string
	auth   map[string]string
	params string
	result string
	err    error
}

func TestWebAPIProfileServicesBindCallerAndCannotGrantFiles(t *testing.T) {
	ex := &fakeExecutor{result: `{"delegation_id":"grant"}`}
	sv := services{executor: ex, webAPI: true, webAPIOnly: true}
	if _, ok := sv.table()["root.run"]; ok {
		t.Fatal("WebAPI capability exposed root.run")
	}
	for _, op := range []string{"webapi.grant", "webapi.grants", "webapi.revoke", "webapi.call"} {
		if reply, _ := svcAs(t, sv, "", op, map[string]any{}); svcError(reply) != "session_required" {
			t.Fatalf("%s: %v", op, reply)
		}
	}
	reply, _ := svcAs(t, sv, "alice", "webapi.grant", map[string]any{"scope": "calendar", "lifetime": "until-revoked"})
	if svcError(reply) != "" || ex.op != "delegate.create" || ex.auth["dsm_cookie"] != "sid1" || ex.params != `{"lifetime":"until-revoked","ops":["webapi.call"],"scope":"calendar"}` {
		t.Fatalf("%v %s %s", reply, ex.op, ex.params)
	}
	if reply, _ := svcAs(t, sv, "alice", "webapi.grant", map[string]any{"scope": "calendar", "ops": []string{"files.read"}}); svcError(reply) != "bad_request" {
		t.Fatal(reply)
	}
}

func (f *fakeExecutor) Call(_ context.Context, op string, auth map[string]string, params any, result any) error {
	f.op, f.auth = op, auth
	b, _ := json.Marshal(params)
	f.params = string(b)
	if f.err != nil {
		return f.err
	}
	return json.Unmarshal([]byte(f.result), result)
}

func TestServicesActOnlyForASignedInCaller(t *testing.T) {
	sv := services{executor: &fakeExecutor{}, jobs: background.NewRequestStore(t.TempDir()), lookupUID: func(string) string { return "1000" }}
	for _, op := range []string{"root.run", "bg.get"} {
		if reply, _ := svcAs(t, sv, "", op, map[string]any{}); svcError(reply) != "session_required" {
			t.Errorf("%s: %v", op, reply)
		}
	}
}

func TestRootForwardsTheRequestsSessionAndTheExecutorsAnswer(t *testing.T) {
	executor := &fakeExecutor{result: `{"request_id":"r1","exit_code":0,"output":"ok","stderr":""}`}
	sv := services{executor: executor}
	reply, _ := svcAs(t, sv, "alice", "root.run", map[string]any{"name": "scan", "params": map[string]any{"deep": true}})
	result, _ := json.Marshal(reply["result"])
	if string(result) != `{"exit_code":0,"output":"ok","request_id":"r1","stderr":""}` {
		t.Fatalf("reply %v", reply)
	}
	if executor.op != "root.run" || executor.auth["dsm_cookie"] != "sid1" || executor.auth["dsm_synotoken"] != "tok1" || executor.params != `{"name":"scan","params":{"deep":true}}` {
		t.Fatalf("executor got %s %v %s", executor.op, executor.auth, executor.params)
	}
	executor.err = &api.Error{Code: "ROOT_BUSY", Message: "busy"}
	if reply, _ := svcAs(t, sv, "alice", "root.start", map[string]any{"name": "scan"}); svcError(reply) != "ROOT_BUSY" {
		t.Fatalf("busy %v", reply)
	}
	if reply, _ := svcAs(t, sv, "alice", "root.run", []int{1}); svcError(reply) != "bad_request" {
		t.Fatalf("array params %v", reply)
	}
}

// readDSM is File Station serving one file to alice.
type readDSM struct {
	filestation.DSMClient
	body string
}

func (d readDSM) Verify(_ context.Context, _ *http.Request, user string) error {
	if user != "alice" {
		return &api.Error{Code: "permission_denied", Message: "no"}
	}
	return nil
}

func (d readDSM) DownloadStream(context.Context, *http.Request, string) (filestation.Download, error) {
	return filestation.Download{Body: io.NopCloser(strings.NewReader(d.body)), ContentLength: int64(len(d.body))}, nil
}

func TestFilesReadSpoolsTheFileForTheCall(t *testing.T) {
	sv := services{files: filestation.NewService(readDSM{body: "hello"}), maxSpool: 5}
	reply, spooled := svcAs(t, sv, "alice", "files.read", map[string]any{"path": "/home/a.txt"})
	if result, _ := reply["result"].(map[string]any); spooled != "hello" || result["size"] != float64(5) {
		t.Fatalf("reply %v spooled %q", reply, spooled)
	}
	if reply, _ := svcAs(t, sv, "bob", "files.read", map[string]any{"path": "/home/a.txt"}); svcError(reply) != "permission_denied" {
		t.Fatalf("bob %v", reply)
	}
	sv.files = filestation.NewService(readDSM{body: "hello!"})
	if reply, _ := svcAs(t, sv, "alice", "files.read", map[string]any{"path": "/home/a.txt"}); svcError(reply) != "limit_exceeded" {
		t.Fatalf("big %v", reply)
	}
}

func TestJobsBelongToTheCaller(t *testing.T) {
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "in"), []byte("abc"), 0o600)
	sv := services{dataDir: data, jobs: background.NewRequestStore(filepath.Join(data, "background")), lookupUID: func(user string) string {
		return map[string]string{"alice": "1001", "bob": "1002"}[user]
	}}
	reply, _ := svcAs(t, sv, "alice", "bg.submit", map[string]any{"key": "k1", "name": "hash", "input": filepath.Join(data, "in")})
	result, _ := reply["result"].(map[string]any)
	job, _ := result["job"].(map[string]any)
	id, _ := job["id"].(string)
	if id == "" || result["duplicate"] != false {
		t.Fatalf("submit %v", reply)
	}
	if reply, _ := svcAs(t, sv, "alice", "bg.get", map[string]any{"id": id}); reply["result"].(map[string]any)["id"] != id {
		t.Fatalf("alice get %v", reply)
	}
	if reply, _ := svcAs(t, sv, "bob", "bg.get", map[string]any{"id": id}); svcError(reply) != "not_found" {
		t.Fatalf("bob get %v", reply)
	}
	for name, input := range map[string]string{"outside": "/etc/passwd", "relative": "in", "escape": filepath.Join(data, "../x")} {
		if reply, _ := svcAs(t, sv, "alice", "bg.submit", map[string]any{"key": "k2", "name": "hash", "input": input}); svcError(reply) != "bad_request" {
			t.Errorf("%s: %v", name, reply)
		}
	}
}

func TestPortalStatusService(t *testing.T) {
	sv := services{portal: func() portal.Status { return portal.Status{} }}
	if reply, _ := svcAs(t, sv, "", "portal.status", nil); reply["result"] == nil {
		t.Fatalf("reply %v", reply)
	}
	if reply, _ := svcAs(t, sv, "", "portal.status", map[string]any{"x": 1}); svcError(reply) != "bad_request" {
		t.Fatalf("params %v", reply)
	}
}

// The executor socket is the package's unless a Go test sets
// Config.ExecutorSocket; the package CGI never sets it.
func TestExecutorSocketIsThePackagesUnlessATestSetsIt(t *testing.T) {
	m := Manifest{Capabilities: Capabilities{Root: true, Delegate: &DelegateCapability{}}}
	for socket, want := range map[string]string{"": "/run/synopkgland/hello/executor.sock", "/tmp/e2e/executor.sock": "/tmp/e2e/executor.sock"} {
		sv := Config{PackageID: "hello", DataDir: t.TempDir(), ExecutorSocket: socket}.services(m)
		if got := sv.executor.(*delegate.Client).Socket; got != want {
			t.Errorf("root.* executor %q, want %q", got, want)
		}
		// The client waits per op: delegate.RootRunTimeout for root.run,
		// delegate.ExecutorTimeout for the job ops the executor answers in 30 s.
		if got := sv.executor.(*delegate.Client).Timeout; got != delegate.ExecutorTimeout {
			t.Errorf("root.* executor waits %v, want %v", got, delegate.ExecutorTimeout)
		}
		if got := sv.delegate.Client.(*delegate.Client).Socket; got != want {
			t.Errorf("delegate executor %q, want %q", got, want)
		}
	}
}
