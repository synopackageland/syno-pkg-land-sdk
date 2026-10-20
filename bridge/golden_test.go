//go:build unix

package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// The cross-language runner: the same api golden requests go through the
// bridge to a Go, a Python, a Node and a Rust App in exec mode, and
// every reply must match testdata/golden/api.jsonl, which api's own golden
// test pins to the native Go server.

type goldenCase struct {
	Name        string            `json:"name"`
	Auth        string            `json:"auth"`
	Method      string            `json:"method"`
	Target      string            `json:"target"`
	ContentType string            `json:"contentType"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body"`
	Status      int               `json:"status"`
	Reply       map[string]string `json:"reply"`
	ReplyBody   string            `json:"replyBody"`
}

// notBridged: the fixture's demo.download writes the caller's SynoToken
// into the reply; the session never reaches an App, and the prototype has
// no transfer channel yet.
var notBridged = map[string]bool{"download without origin": true, "upload without origin": true, "download form": true}

// numberLiterals: a native Go handler can echo params byte for byte; a Python,
// Node or Go-map App gets values, so 2.50 comes back as 2.5 (spec/bridge/v1
// README, Numbers). The Rust App SDK hands over the raw params and keeps it.
var numberLiterals = map[string][2]string{"echo params": {"2.50", "2.5"}}

// goldenMethods are the fixture's methods: the api cases' and the bridge's
// own (golden_bridge_test.go).
var goldenMethods = []string{"demo.echo", "demo.order", "demo.fail", "demo.nothing", "demo.admin", "demo.ping", "demo.fetch", "demo.svc", "demo.svc_public", "demo.upload"}

type goldenAuth struct{ mode string }

// Authenticate matches goldenAuth in api/golden_test.go.
func (a goldenAuth) Authenticate(*http.Request) (api.Identity, error) {
	switch a.mode {
	case "no-session":
		return api.Identity{}, api.ErrNoSession
	case "app-denied":
		return api.Identity{}, api.ErrAppPrivilegeDenied
	case "admin":
		return api.Identity{User: "root", Admin: true}, nil
	}
	return api.Identity{User: a.mode}, nil
}

// goldenGoApp is the fixture as a Go App on the channel.
var goldenGoApp = fakeApp{methods: goldenMethods, handle: func(c *fakeConn, call map[string]any) map[string]any {
	ctx := call["ctx"].(map[string]any)
	switch call["method"] {
	case "demo.echo":
		var raw struct{ Params json.RawMessage }
		json.Unmarshal(c.last, &raw)
		return map[string]any{"ok": true, "data": json.RawMessage(`{"params":` + values(raw.Params) + `,"user":` + string(mustJSON(ctx["user"])) + `}`)}
	case "demo.order":
		return map[string]any{"ok": true, "data": json.RawMessage(`{"zeta":"<a href=\"x\">&</a>","alpha":"\u2028\u2029\t\u0001é","empty":null}`)}
	case "demo.fail":
		return map[string]any{"ok": false, "error": map[string]any{"code": "conflict", "message": "a <b> & c"}}
	case "demo.nothing":
		return map[string]any{"ok": true, "data": nil}
	case "demo.admin":
		return map[string]any{"ok": true, "data": true}
	}
	return map[string]any{"ok": true, "data": "pong"}
}}

// values re-encodes JSON as an App sees it: member order kept, numbers as
// values (2.50 becomes 2.5).
func values(raw json.RawMessage) string {
	var out strings.Builder
	writeValue(json.NewDecoder(bytes.NewReader(raw)), &out)
	return out.String()
}

func writeValue(dec *json.Decoder, out *strings.Builder) {
	tok, _ := dec.Token()
	d, ok := tok.(json.Delim)
	if !ok {
		out.Write(mustJSON(tok))
		return
	}
	out.WriteString(d.String())
	for i := 0; dec.More(); i++ {
		if i > 0 {
			out.WriteByte(',')
		}
		if d == '{' {
			key, _ := dec.Token()
			out.Write(mustJSON(key))
			out.WriteByte(':')
		}
		writeValue(dec, out)
	}
	end, _ := dec.Token()
	out.WriteString(end.(json.Delim).String())
}

func goldenServer(t *testing.T, mode string, d Dialer) http.Handler {
	routes := append(Routes(&Session{Dial: d, Methods: goldenMethods}),
		api.HandleStream("demo.download", func(context.Context, api.Caller, http.ResponseWriter, *http.Request) error {
			return api.BadRequest("not bridged")
		}))
	policy := `{"schema":1,"default":"administrators","methods":{"demo.echo":"authenticated","demo.order":"authenticated","demo.fail":"authenticated","demo.nothing":"authenticated","demo.ping":"public","demo.download":"authenticated"}}`
	srv, err := api.NewServerWithPermissions(goldenAuth{mode}, []byte(policy), routes...)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func goldenCases(t *testing.T, literals bool) []goldenCase {
	f, err := os.Open("../testdata/golden/api.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var cases []goldenCase
	s := bufio.NewScanner(f)
	for s.Scan() {
		var c goldenCase
		if err := json.Unmarshal(s.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		if notBridged[c.Name] {
			continue
		}
		if swap, ok := numberLiterals[c.Name]; ok && !literals {
			c.ReplyBody = strings.Replace(c.ReplyBody, swap[0], swap[1], 1)
			c.Reply["Content-Length"] = itoa(len(c.ReplyBody))
		}
		cases = append(cases, c)
	}
	return cases
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func replay(t *testing.T, d Dialer, c goldenCase) {
	t.Helper()
	r := httptest.NewRequest(c.Method, "https://nas.local:5001"+c.Target, strings.NewReader(c.Body))
	r.Header.Set("Content-Type", c.ContentType)
	for k, v := range c.Headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	goldenServer(t, c.Auth, d).ServeHTTP(w, r)
	reply := map[string]string{}
	for k := range c.Reply {
		reply[k] = w.Header().Get(k)
	}
	if w.Code != c.Status || w.Body.String() != c.ReplyBody || !bytes.Equal(mustJSON(reply), mustJSON(c.Reply)) {
		t.Errorf("%s:\n got %d %v %s\nwant %d %v %s", c.Name, w.Code, reply, w.Body, c.Status, c.Reply, c.ReplyBody)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// app is one language's fixture: how to start it.
type app struct {
	name     string
	literals bool // keeps number literals
	cmd      func(t *testing.T) (string, []string, []string)
}

// rustExamplesEnv names the directory with the Rust App SDK's built examples
// (golden, hello); scripts/gate.sh builds them.
const rustExamplesEnv = "SYNOPKGLAND_RUST_APP_EXAMPLES"

func rustExample(t *testing.T, name string) string {
	dir := os.Getenv(rustExamplesEnv)
	if dir == "" {
		t.Skip(rustExamplesEnv + " is not set")
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s: %v", rustExamplesEnv, err)
	}
	return path
}

func root(t *testing.T) string {
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func interpreter(t *testing.T, name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is required for the cross-language runner: %v", name, err)
	}
	return path
}

var goldenApps = []app{
	{"python", false, func(t *testing.T) (string, []string, []string) {
		return interpreter(t, "python3"), []string{"bridge/testdata/golden_app.py"}, []string{"PYTHONPATH=" + filepath.Join(root(t), "python"), "PYTHONDONTWRITEBYTECODE=1"}
	}},
	{"node", false, func(t *testing.T) (string, []string, []string) {
		return interpreter(t, "node"), []string{"bridge/testdata/golden_app.mjs"}, nil
	}},
	{"rust", true, func(t *testing.T) (string, []string, []string) {
		return rustExample(t, "golden"), nil, nil
	}},
}

func TestGoldenRepliesMatchInEveryLanguage(t *testing.T) {
	t.Run("go", func(t *testing.T) {
		for _, c := range goldenCases(t, false) {
			replay(t, pipeDialer(goldenGoApp), c)
		}
	})
	for _, a := range goldenApps {
		t.Run(a.name, func(t *testing.T) {
			path, args, env := a.cmd(t)
			d := &Exec{Path: path, Args: args, Dir: root(t), Env: env}
			for _, c := range goldenCases(t, a.literals) {
				replay(t, d, c)
			}
		})
	}
}
