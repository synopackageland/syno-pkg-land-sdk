//go:build unix

package bridge

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"github.com/synopackageland/syno-pkg-land-sdk/portal"
)

// The bridge's own cases for the cross-language runner: services and
// transfers. The same requests reach a Go, a Python, a Node and a Rust App,
// and every reply must be the one below, byte for byte.

// bridgeGoApp is the bridge fixture as a Go App: demo.svc runs the service
// the params name and answers its result or its refusal.
var bridgeGoApp = fakeApp{methods: goldenMethods, handle: func(c *fakeConn, call map[string]any) map[string]any {
	var in struct{ Params json.RawMessage }
	json.Unmarshal(c.last, &in)
	ctx := call["ctx"].(map[string]any)
	switch call["method"] {
	case "demo.fail":
		return goldenGoApp.handle(c, call)
	case "demo.upload":
		var p struct {
			Query  json.RawMessage
			Upload struct {
				Path string
				Size int64
			}
		}
		json.Unmarshal(in.Params, &p)
		content, _ := os.ReadFile(p.Upload.Path)
		return map[string]any{"ok": true, "data": json.RawMessage(`{"query":` + values(p.Query) + `,"size":` + itoa(int(p.Upload.Size)) + `,"content":` + string(mustJSON(string(content))) + `}`)}
	case "demo.fetch":
		var p struct{ Download struct{ Path string } }
		json.Unmarshal(in.Params, &p)
		os.WriteFile(p.Download.Path, []byte("hello "+ctx["user"].(string)), 0o600)
		return map[string]any{"ok": true, "data": json.RawMessage(`{"name":"a b.txt","content_type":"text/plain; charset=utf-8"}`)}
	}
	var p struct {
		Op     string
		Params json.RawMessage
	}
	json.Unmarshal(in.Params, &p)
	var params any
	if len(p.Params) > 0 {
		params = p.Params
	}
	c.svc(p.Op, params)
	var r struct {
		OK            bool
		Result, Error json.RawMessage
	}
	json.Unmarshal(c.last, &r)
	if r.OK {
		return map[string]any{"ok": true, "data": json.RawMessage(`{"result":` + values(r.Result) + `}`)}
	}
	return map[string]any{"ok": true, "data": json.RawMessage(`{"error":` + values(r.Error) + `}`)}
}}

// goldenExecutor answers root.* as an executor would.
type goldenExecutor struct{}

func (goldenExecutor) Call(_ context.Context, op string, auth map[string]string, _ any, result any) error {
	switch {
	case auth["dsm_cookie"] != "sid1":
		return &api.Error{Code: "AUTH_INVALID", Message: "not the request's session"}
	case op == "root.run":
		return json.Unmarshal([]byte(`{"request_id":"r1","exit_code":0,"output":"<b>é</b>\u2028","stderr":""}`), result)
	case op == "root.start":
		return &api.Error{Code: "limit-exceeded", Message: "params too large"}
	}
	return &api.Error{Code: "ROOT_BUSY", Message: "another root job runs"}
}

func bridgeServer(t *testing.T, d Dialer, auth string) http.Handler {
	t.Helper()
	data := t.TempDir()
	sv := services{dataDir: data, portal: func() portal.Status { return portal.Status{State: "enabled", Alias: "hello"} },
		jobs: background.NewRequestStore(filepath.Join(data, "background")), executor: goldenExecutor{},
		lookupUID: func(string) string { return "1001" }}
	session := &Session{Dial: d, Methods: goldenMethods, Services: sv.table(), Spool: filepath.Join(data, SpoolDir),
		Transfers: map[string]string{"demo.upload": "upload", "demo.fetch": "download", "demo.fail": "download"}}
	policy := `{"schema":1,"default":"administrators","methods":{"demo.svc":"authenticated","demo.svc_public":"public","demo.upload":"authenticated","demo.fetch":"authenticated","demo.fail":"authenticated"}}`
	srv, err := api.NewServerWithPermissions(goldenAuth{auth}, []byte(policy), Routes(session)...)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

type bridgeCase struct {
	name, auth, method, target, contentType, body string
	status                                        int
	reply                                         map[string]string
	replyBody                                     string
}

func svcCase(name, method, op, params, want string) bridgeCase {
	body := `{"method":"` + method + `","params":{"op":"` + op + `"` + params + `}}`
	return bridgeCase{name: name, auth: "alice", method: "POST", target: "", contentType: "application/json", body: body, status: 200,
		replyBody: `{"data":` + want + `,"success":true}` + "\n"}
}

var bridgeCases = []bridgeCase{
	{name: "download app refusal", auth: "alice", method: "GET", target: "?method=demo.fail", status: 500,
		replyBody: `{"error":{"code":"conflict","message":"a \u003cb\u003e \u0026 c"},"success":false}` + "\n"},
	{name: "download no session", auth: "no-session", method: "GET", target: "?method=demo.fetch", status: 401,
		replyBody: `{"error":{"code":"session_required","message":"sign in to DSM again"},"success":false}` + "\n"},
	{name: "download invalid query", auth: "alice", method: "GET", target: "?method=demo.fetch&a=1&a=2", status: 400,
		replyBody: `{"error":{"code":"bad_request","message":"a query parameter is repeated"},"success":false}` + "\n"},
	svcCase("portal.status", "demo.svc", "portal.status", ``, `{"result":{"state":"enabled","alias":"hello"}}`),
	svcCase("root.run forwards the session", "demo.svc", "root.run", `,"params":{"name":"scan","params":{"deep":true}}`,
		`{"result":{"request_id":"r1","exit_code":0,"output":"\u003cb\u003eé\u003c/b\u003e\u2028","stderr":""}}`),
	svcCase("executor code fits the frame", "demo.svc", "root.start", `,"params":{"name":"scan"}`,
		`{"error":{"code":"limit_exceeded","message":"params too large"}}`),
	svcCase("executor refusal", "demo.svc", "root.jobs.list", `,"params":{}`,
		`{"error":{"code":"ROOT_BUSY","message":"another root job runs"}}`),
	svcCase("not the caller's job", "demo.svc", "bg.get", `,"params":{"id":"nope"}`,
		`{"error":{"code":"not_found","message":"Executor operation refused (not_found)"}}`),
	svcCase("strict params", "demo.svc", "bg.get", `,"params":{"id":"x","user":"bob"}`,
		`{"error":{"code":"bad_request","message":"invalid svc params"}}`),
	svcCase("unknown service", "demo.svc", "no.such", ``,
		`{"error":{"code":"unknown_service","message":"the runtime does not offer no.such"}}`),
	func() bridgeCase {
		c := svcCase("public call has no one to act for", "demo.svc_public", "root.run", `,"params":{"name":"scan"}`,
			`{"error":{"code":"session_required","message":"this service acts for a signed-in user"}}`)
		c.auth = "no-session"
		return c
	}(),
	{name: "upload", auth: "alice", method: "POST", target: "?method=demo.upload&b=2&a=%3C1%3E", contentType: "application/octet-stream", body: "héllo", status: 200,
		replyBody: `{"data":{"query":{"a":"\u003c1\u003e","b":"2"},"size":6,"content":"héllo"},"success":true}` + "\n"},
	{name: "download", auth: "alice", method: "GET", target: "?method=demo.fetch", status: 200,
		reply: map[string]string{"Content-Type": "text/plain; charset=utf-8", "Content-Disposition": `attachment; filename="a b.txt"`, "Content-Length": "11",
			"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff"},
		replyBody: "hello alice"},
}

func replayBridge(t *testing.T, d Dialer, c bridgeCase) {
	t.Helper()
	r := httptest.NewRequest(c.method, "https://nas.local:5001/api.cgi"+c.target, strings.NewReader(c.body))
	if c.contentType != "" {
		r.Header.Set("Content-Type", c.contentType)
	}
	r.Header.Set("Origin", "https://nas.local:5001")
	for k, v := range dsmHeaders {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	bridgeServer(t, d, c.auth).ServeHTTP(w, r)
	reply := map[string]string{}
	for k := range c.reply {
		reply[k] = w.Header().Get(k)
	}
	if w.Code != c.status || w.Body.String() != c.replyBody || !maps.Equal(reply, c.reply) {
		t.Errorf("%s:\n got %d %v %s\nwant %d %v %s", c.name, w.Code, reply, w.Body, c.status, c.reply, c.replyBody)
	}
}

func TestBridgeGoldenRepliesMatchInEveryLanguage(t *testing.T) {
	t.Run("go", func(t *testing.T) {
		for _, c := range bridgeCases {
			replayBridge(t, pipeDialer(bridgeGoApp), c)
		}
	})
	for _, a := range goldenApps {
		t.Run(a.name, func(t *testing.T) {
			path, args, env := a.cmd(t)
			d := &Exec{Path: path, Args: args, Dir: root(t), Env: env}
			for _, c := range bridgeCases {
				replayBridge(t, d, c)
			}
		})
	}
}
