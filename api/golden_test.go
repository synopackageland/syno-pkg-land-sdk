package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// goldenPath holds request/reply pairs that bridge/golden_test.go replays
// against the same fixture routes as Python, Node and Rust Apps, so every App
// answers the frontend byte for byte.
const goldenPath = "../testdata/golden/api.jsonl"

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

type goldenAuth struct{ mode string }

func (a goldenAuth) Authenticate(*http.Request) (Identity, error) {
	switch a.mode {
	case "no-session":
		return Identity{}, ErrNoSession
	case "app-denied":
		return Identity{}, ErrAppPrivilegeDenied
	case "admin":
		return Identity{User: "root", Admin: true}, nil
	}
	return Identity{User: a.mode}, nil
}

type goldenOrder struct {
	Zeta  string   `json:"zeta"`
	Alpha string   `json:"alpha"`
	Empty []string `json:"empty"`
	Skip  string   `json:"skip,omitempty"`
}

// goldenServer must match the bridge golden Apps (bridge/testdata, rust/app/examples/golden.rs).
func goldenServer(t *testing.T, mode string) *Server {
	t.Helper()
	routes := []Route{
		Handle("demo.echo", func(_ context.Context, c Caller, p json.RawMessage) (any, error) {
			return map[string]any{"user": c.User(), "params": p}, nil
		}),
		Handle("demo.order", func(context.Context, Caller, json.RawMessage) (any, error) {
			return goldenOrder{Zeta: "<a href=\"x\">&</a>", Alpha: "  \t\x01é"}, nil
		}),
		Handle("demo.fail", func(context.Context, Caller, json.RawMessage) (any, error) {
			return nil, &Error{"conflict", "a <b> & c"}
		}),
		Handle("demo.nothing", func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil }),
		Handle("demo.admin", func(context.Context, Caller, json.RawMessage) (any, error) { return true, nil }),
		Handle("demo.ping", func(context.Context, Caller, json.RawMessage) (any, error) { return "pong", nil }),
		HandleStream("demo.download", func(_ context.Context, c Caller, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, err := w.Write([]byte(c.User() + ":" + r.Header.Get("X-Syno-Token")))
			return err
		}),
	}
	policy := `{"schema":1,"default":"administrators","methods":{"demo.echo":"authenticated","demo.order":"authenticated","demo.fail":"authenticated","demo.nothing":"authenticated","demo.ping":"public","demo.download":"authenticated"}}`
	srv, err := NewServerWithPermissions(goldenAuth{mode}, []byte(policy), routes...)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func goldenInputs() []goldenCase {
	user, json := "alice", "application/json"
	post := func(name, body string) goldenCase {
		return goldenCase{Name: name, Auth: user, Method: "POST", Target: "/api.cgi", ContentType: json, Headers: map[string]string{"Origin": "https://nas.local:5001"}, Body: body}
	}
	with := func(c goldenCase, change func(*goldenCase)) goldenCase { change(&c); return c }
	return []goldenCase{
		post("echo params", `{"method":"demo.echo","params":{ "b" : [1, 2.50, "<x>"], "a": null }}`),
		post("echo null params", `{"method":"demo.echo","params":null}`),
		post("echo absent params", `{"method":"demo.echo"}`),
		post("struct field order and escapes", `{"method":"demo.order","params":{}}`),
		post("nil data", `{"method":"demo.nothing"}`),
		post("api error", `{"method":"demo.fail"}`),
		post("unknown method", `{"method":"demo.missing"}`),
		post("admin required", `{"method":"demo.admin"}`),
		with(post("admin allowed", `{"method":"demo.admin"}`), func(c *goldenCase) { c.Auth = "admin" }),
		with(post("no session", `{"method":"demo.echo"}`), func(c *goldenCase) { c.Auth = "no-session" }),
		with(post("app privilege denied", `{"method":"demo.echo"}`), func(c *goldenCase) { c.Auth = "app-denied" }),
		with(post("public without session", `{"method":"demo.ping"}`), func(c *goldenCase) { c.Auth = "no-session" }),
		with(post("GET", `{"method":"demo.echo"}`), func(c *goldenCase) { c.Method = "GET" }),
		with(post("text body", `{"method":"demo.echo"}`), func(c *goldenCase) { c.ContentType = "text/plain" }),
		with(post("json with charset", `{"method":"demo.echo"}`), func(c *goldenCase) { c.ContentType = "application/json; charset=utf-8" }),
		with(post("cross site", `{"method":"demo.echo"}`), func(c *goldenCase) { c.Headers = map[string]string{"Sec-Fetch-Site": "cross-site"} }),
		with(post("same origin", `{"method":"demo.echo"}`), func(c *goldenCase) {
			c.Headers = map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://nas.local:5001"}
		}),
		with(post("no origin", `{"method":"demo.echo"}`), func(c *goldenCase) { c.Headers = nil }),
		with(post("referer of the host", `{"method":"demo.echo"}`), func(c *goldenCase) {
			c.Headers = map[string]string{"Referer": "https://nas.local:5001/webman/3rdparty/App/index.html"}
		}),
		with(post("foreign referer", `{"method":"demo.echo"}`), func(c *goldenCase) { c.Headers = map[string]string{"Referer": "https://evil.example/"} }),
		with(post("opaque origin", `{"method":"demo.echo"}`), func(c *goldenCase) { c.Headers = map[string]string{"Origin": "null"} }),
		with(post("same site", `{"method":"demo.echo"}`), func(c *goldenCase) {
			c.Headers = map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://nas.local:8443"}
		}),
		with(post("forwarded host", `{"method":"demo.echo"}`), func(c *goldenCase) {
			c.Headers = map[string]string{"Origin": "https://nas.example.com", "X-Forwarded-Host": "nas.example.com"}
		}),
		with(post("foreign origin with forwarded host", `{"method":"demo.echo"}`), func(c *goldenCase) {
			c.Headers = map[string]string{"Origin": "https://evil.example", "X-Forwarded-Host": "nas.example.com"}
		}),
		with(post("browser attests same origin", `{"method":"demo.echo"}`), func(c *goldenCase) {
			c.Headers = map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://nas.example.com"}
		}),
		with(post("download without origin", ""), func(c *goldenCase) {
			c.Method, c.Target, c.ContentType, c.Headers = "GET", "/api.cgi?method=demo.download", "", nil
		}),
		with(post("upload without origin", "data"), func(c *goldenCase) {
			c.Target, c.ContentType, c.Headers = "/api.cgi?method=demo.download", "application/octet-stream", nil
		}),
		post("too deep", `{"method":"demo.echo","params":`+strings.Repeat("[", 33)+strings.Repeat("]", 33)+`}`),
		post("malformed", `{"method":`),
		post("unknown field", `{"method":"demo.echo","user":"root"}`),
		post("empty method", `{"method":""}`),
		post("stream over JSON", `{"method":"demo.download"}`),
		with(post("download form", "SynoToken=tok%2B1"), func(c *goldenCase) {
			c.Target, c.ContentType = "/api.cgi?method=demo.download", "application/x-www-form-urlencoded"
		}),
		with(post("download duplicate token", "SynoToken=a&SynoToken=b"), func(c *goldenCase) {
			c.Target, c.ContentType = "/api.cgi?method=demo.download", "application/x-www-form-urlencoded"
		}),
		with(post("JSON method as transfer", ""), func(c *goldenCase) { c.Target = "/api.cgi?method=demo.echo" }),
	}
}

func runGolden(t *testing.T, c goldenCase) goldenCase {
	r := httptest.NewRequest(c.Method, "https://nas.local:5001"+c.Target, strings.NewReader(c.Body))
	r.Header.Set("Content-Type", c.ContentType)
	for k, v := range c.Headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	goldenServer(t, c.Auth).ServeHTTP(w, r)
	c.Status, c.ReplyBody, c.Reply = w.Code, w.Body.String(), map[string]string{}
	for _, k := range []string{"Content-Type", "Content-Length", "Cache-Control", "X-Content-Type-Options"} {
		if v := w.Header().Get(k); v != "" {
			c.Reply[k] = v
		}
	}
	return c
}

func TestGoldenRepliesMatchSharedFile(t *testing.T) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	for _, c := range goldenInputs() {
		if err := enc.Encode(runGolden(t, c)); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("SYNOPKGLAND_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("%s is stale; regenerate with SYNOPKGLAND_UPDATE_GOLDEN=1 go test ./api -run Golden", goldenPath)
	}
}
