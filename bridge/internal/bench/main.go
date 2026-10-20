// Command bench times one hello.greet request through api.Server, natively
// in Go and through the bridge to the hello Apps in exec mode. The CGI
// process start is the same for all of them and is not included, except in
// the end-to-end rows of -cgi, which run the Go runtime as a CGI.
//
//	bench -root DIR [-n 5] [-python python3] [-node node] [-rust hello [-cgi]]
//
// DIR holds python/ and node/ as in the repository; -rust names the Rust
// hello example.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/bridge"
)

type auth struct{}

func (auth) Authenticate(*http.Request) (api.Identity, error) {
	return api.Identity{User: "alice"}, nil
}

const reply = `{"data":{"message":"Hello, alice"},"success":true}` + "\n"

const policy = `{"schema":1,"default":"administrators","methods":{"hello.greet":"authenticated","hello.save":"authenticated"}}`

func server(routes ...api.Route) http.Handler {
	srv, err := api.NewServerWithPermissions(auth{}, []byte(policy), routes...)
	if err != nil {
		log.Fatal(err)
	}
	return srv
}

func request(h http.Handler) error {
	r := httptest.NewRequest("POST", "https://nas.local/api.cgi", strings.NewReader(greet))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://nas.local")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Body.String() != reply {
		return fmt.Errorf("reply %s", w.Body)
	}
	return nil
}

func timeIt(n int, f func() error) []time.Duration {
	out := make([]time.Duration, 0, n)
	for range n {
		start := time.Now()
		if err := f(); err != nil {
			log.Fatal(err)
		}
		out = append(out, time.Since(start))
	}
	return out
}

func report(name string, d []time.Duration) {
	s := slices.Clone(d)
	slices.Sort(s)
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	all := make([]string, len(d))
	for i, v := range d {
		all[i] = fmt.Sprintf("%.2f", ms(v))
	}
	fmt.Printf("%-22s median %8.2f ms  max %8.2f ms  [%s]\n", name, ms(s[len(s)/2]), ms(s[len(s)-1]), strings.Join(all, " "))
}

func main() {
	if os.Getenv("REQUEST_METHOD") != "" {
		serveCGI()
		return
	}
	root := flag.String("root", ".", "directory with python/ and node/")
	n := flag.Int("n", 5, "samples per row")
	python := flag.String("python", "python3", "Python interpreter")
	node := flag.String("node", "node", "Node interpreter")
	rust := flag.String("rust", "", "Rust hello example; none to skip")
	cgi := flag.Bool("cgi", false, "also time whole CGI requests, natively and in front of the Rust App; needs -rust")
	flag.Parse()
	abs, _ := filepath.Abs(*root)
	native := nativeServer()
	report("go native", timeIt(*n, func() error { return request(native) }))

	apps := []app{
		{"python", *python, []string{"python/examples/hello/app.py"}, []string{"PYTHONPATH=" + filepath.Join(abs, "python"), "PYTHONDONTWRITEBYTECODE=1"}},
		{"node", *node, []string{"node/examples/hello/app.mjs"}, nil},
	}
	if *rust != "" {
		bin, _ := filepath.Abs(*rust)
		apps = append(apps, app{"rust", bin, nil, nil})
	}
	for _, a := range apps {
		if arg, ok := map[string][]string{"python": {"-c", "pass"}, "node": {"-e", "0"}}[a.name]; ok {
			report(a.name+" interpreter", timeIt(*n, func() error { return exec.Command(a.interp, arg...).Run() }))
		}
		exe := &bridge.Exec{Path: a.interp, Args: a.args, Dir: abs, Env: a.env}
		h := server(bridge.Routes(&bridge.Session{Dial: exe, Methods: methods})...)
		report(a.name+" exec", timeIt(*n, func() error { return request(h) }))
	}
	if *cgi && *rust != "" {
		endToEnd(*n, apps[len(apps)-1])
	}
}

var methods = []string{"hello.greet", "hello.save"}

// app is a hello App: the command that runs it from the repository root.
type app struct {
	name, interp string
	args, env    []string
}
