package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/bridge"
)

const greet = `{"method":"hello.greet"}`

// appVar tells the bench, started as a CGI, what serves hello: native, or
// exec:<App> through the bridge.
const appVar = "SYNOPKGLAND_BENCH_APP"

// cgiRequest runs bin as DSM's web server runs a CGI, with request()'s
// request, and checks the reply.
func cgiRequest(bin string, env ...string) error {
	cmd := exec.Command(bin)
	cmd.Env = append([]string{
		"REQUEST_METHOD=POST", "SERVER_PROTOCOL=HTTP/1.1", "REQUEST_URI=/api.cgi",
		"HTTP_HOST=nas.local", "HTTP_ORIGIN=https://nas.local", "HTTPS=on",
		"CONTENT_TYPE=application/json", fmt.Sprintf("CONTENT_LENGTH=%d", len(greet)),
		"REMOTE_ADDR=192.0.2.1", "REMOTE_PORT=1234",
	}, env...)
	cmd.Stdin, cmd.Stderr = strings.NewReader(greet), os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	if !bytes.HasSuffix(out, []byte("\n"+reply)) {
		return fmt.Errorf("cgi reply %q", out)
	}
	return nil
}

// serveCGI is the bench started by cgiRequest: the Go runtime as a CGI.
func serveCGI() {
	var h http.Handler
	switch app := os.Getenv(appVar); {
	case app == "native":
		h = nativeServer()
	case strings.HasPrefix(app, "exec:"):
		h = server(bridge.Routes(&bridge.Session{Dial: &bridge.Exec{Path: app[len("exec:"):]}, Methods: methods})...)
	default:
		log.Fatalf("%s=%q", appVar, app)
	}
	if err := cgi.Serve(h); err != nil {
		log.Fatal(err)
	}
}

func nativeServer() http.Handler {
	return server(api.Handle("hello.greet", func(_ context.Context, c api.Caller, _ json.RawMessage) (any, error) {
		return map[string]string{"message": "Hello, " + c.User()}, nil
	}), api.Handle("hello.save", func(context.Context, api.Caller, json.RawMessage) (any, error) { return nil, nil }))
}

// endToEnd times a whole CGI request: the Go runtime CGI natively and in
// front of the Rust App.
func endToEnd(n int, rust app) {
	self, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	report("go cgi native", timeIt(n, func() error { return cgiRequest(self, appVar+"=native") }))
	report("go cgi + rust exec", timeIt(n, func() error { return cgiRequest(self, appVar+"=exec:"+rust.interp) }))
}
