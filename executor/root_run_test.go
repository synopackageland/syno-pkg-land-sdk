package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var errKilled = errors.New("killed")

// fakeRoot runs each started program as a function; Kill cancels its context.
type fakeRoot struct {
	mu    sync.Mutex
	calls []rootCall
	kills int
	run   func(ctx context.Context, c rootCall) (int, error)
}

type fakeProcess struct {
	root   *fakeRoot
	cancel context.CancelFunc
	done   chan struct{}
	code   int
	err    error
}

func (f *fakeRoot) Start(c rootCall) (rootProcess, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	// Like a child, the program holds its own copies of the passed files and
	// closes them when it ends.
	files := c.Files
	c.Files = nil
	for _, file := range files {
		fd, err := syscall.Dup(int(file.Fd()))
		if err != nil {
			return nil, err
		}
		c.Files = append(c.Files, os.NewFile(uintptr(fd), file.Name()))
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &fakeProcess{root: f, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		defer func() {
			for _, file := range c.Files {
				file.Close()
			}
		}()
		if f.run != nil {
			p.code, p.err = f.run(ctx, c)
		}
	}()
	return p, nil
}

func (f *fakeRoot) started() []rootCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]rootCall(nil), f.calls...)
}

func (p *fakeProcess) Wait() (int, error) { <-p.done; return p.code, p.err }
func (p *fakeProcess) Group() int         { return 0 }

func (f *fakeRoot) killed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.kills
}

func (p *fakeProcess) Kill() {
	p.root.mu.Lock()
	p.root.kills++
	p.root.mu.Unlock()
	p.cancel()
}

// dirStaging is the package staging folder in a test directory.
type dirStaging struct{ dir string }

func (s dirStaging) Available() (int64, error) { return maxWebAPIStaging, nil }

func (s dirStaging) Create() (*os.File, string, error) {
	f, err := os.CreateTemp(s.dir, "root-")
	if err != nil {
		return nil, "", err
	}
	return f, filepath.Base(f.Name()), nil
}
func (s dirStaging) Remove(name string) { os.Remove(filepath.Join(s.dir, name)) }
func (s dirStaging) Used() (int64, error) {
	var n int64
	entries, err := os.ReadDir(s.dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && strings.HasPrefix(e.Name(), "root-") {
			n += info.Size()
		}
	}
	return n, err
}

func rootSetup(t *testing.T, root string) (*Executor, *fakeDSM, *fakeRoot, string) {
	t.Helper()
	e, f, _ := setup(t)
	f.user.Admin = true
	p, err := rootPolicy(t, root)
	if err != nil {
		t.Fatal(err)
	}
	e.Policy.Root = p.Root
	r := &fakeRoot{}
	e.Runner = r
	staging := t.TempDir()
	e.RootStaging = dirStaging{staging}
	return e, f, r, staging
}

func result(t *testing.T, out Response) map[string]any {
	t.Helper()
	if !out.OK {
		t.Fatalf("%+v", out)
	}
	b, _ := json.Marshal(out.Result)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func env(c rootCall) map[string]string {
	out := map[string]string{}
	for _, kv := range c.Env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

const cliSchema = `"schema":{"type":"object","additionalProperties":false,"properties":{"method":{"type":"string","enum":["volumes","doctor"]},"requested_by":{"type":"string"}}}`

func TestRootRunPassesParamsOnStdinAndTheVerifiedCaller(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"cli":{"digest":"`+digestA+`","kind":"binary",`+cliSchema+`,"run":{}}}`)
	r.run = func(_ context.Context, c rootCall) (int, error) {
		io.WriteString(c.Stdout, `{"volumes":[]}`)
		io.WriteString(c.Stderr, "note")
		return 0, nil
	}
	// The caller cannot name another user: requested_by in params is only data.
	out := result(t, request(e, "root.run", map[string]any{"name": "cli", "params": map[string]any{"method": "volumes", "requested_by": "mallory"}}))
	calls := r.started()
	if len(calls) != 1 {
		t.Fatal(calls)
	}
	c := calls[0]
	if c.Name != "cli" || c.Mode != "run" || c.Entry.Kind != "binary" || len(c.Args) != 0 || string(c.Stdin) != `{"method":"volumes","requested_by":"mallory"}` {
		t.Fatalf("%+v", c)
	}
	vars := env(c)
	if vars["SYNOPKGLAND_REQUESTED_BY"] != "alice" || vars["SYNOPKGLAND_PACKAGE"] != "p2probe" || vars["SYNOPKGLAND_ROOT_MODE"] != "run" || !idRE.MatchString(vars["SYNOPKGLAND_REQUEST_ID"]) || len(vars) != 4 {
		t.Fatal(vars)
	}
	if out["exit_code"] != 0.0 || out["output"] != `{"volumes":[]}` || out["stderr"] != "note" || out["request_id"] != vars["SYNOPKGLAND_REQUEST_ID"] {
		t.Fatal(out)
	}
}

func TestRootRunWithoutParamsSendsAnEmptyObject(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"whoami":"`+digestA+`"}`)
	out := result(t, request(e, "root.run", map[string]any{"name": "whoami"}))
	if c := r.started()[0]; string(c.Stdin) != `{}` || c.Entry.Kind != "script" || out["output"] != "" {
		t.Fatalf("%+v %v", c, out)
	}
}

func TestRootRunReturnsTheOutputOfAFailedProgram(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"whoami":"`+digestA+`"}`)
	r.run = func(_ context.Context, c rootCall) (int, error) {
		io.WriteString(c.Stdout, `{"error":"busy"}`)
		return 3, nil
	}
	if out := result(t, request(e, "root.run", map[string]any{"name": "whoami"})); out["exit_code"] != 3.0 || out["output"] != `{"error":"busy"}` {
		t.Fatal(out)
	}
}

func TestRootRunRefusesParamsTheSchemaDoesNot(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"cli":{"digest":"`+digestA+`",`+cliSchema+`,"run":{}},"plain":"`+digestA+`"}`)
	for _, params := range []any{
		map[string]any{"method": "rm"},
		map[string]any{"method": "volumes", "argv": []string{"-rf"}},
		[]string{"volumes"},
		"volumes",
	} {
		if out := request(e, "root.run", map[string]any{"name": "cli", "params": params}); out.Code != "INVALID_REQUEST" {
			t.Fatal(params, out)
		}
	}
	if out := request(e, "root.run", map[string]any{"name": "plain", "params": map[string]any{"a": 1}}); out.Code != "INVALID_REQUEST" {
		t.Fatal(out)
	}
	if out := request(e, "root.run", map[string]any{"name": "cli", "argv": "x"}); out.Code != "INVALID_REQUEST" {
		t.Fatal(out)
	}
	if len(r.started()) != 0 {
		t.Fatal("a refused request started the program")
	}
}

func TestRootRunOnlyRunsEntriesDeclaringRun(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"scan":{"digest":"`+digestA+`","job":{}},"step":{"digest":"`+digestA+`"}}`)
	for _, name := range []string{"scan", "step", "missing", "../x"} {
		if out := request(e, "root.run", map[string]any{"name": name}); out.Code != "POLICY_DENIED" && out.Code != "INVALID_REQUEST" {
			t.Fatal(name, out)
		}
	}
	if len(r.started()) != 0 {
		t.Fatal("started")
	}
}

func TestRootRunNeedsAnAdministratorTheAppAllows(t *testing.T) {
	e, f, r, _ := rootSetup(t, `{"whoami":"`+digestA+`"}`)
	f.user.Admin = false
	if out := request(e, "root.run", map[string]any{"name": "whoami"}); out.Code != "ADMIN_REQUIRED" {
		t.Fatal(out)
	}
	f.user.Admin = true
	f.privilegeErr = errors.New("denied")
	if out := request(e, "root.run", map[string]any{"name": "whoami"}); out.Code != "APP_DENIED" {
		t.Fatal(out)
	}
	if out := e.Handle(context.Background(), Request{Version: 1, Op: "root.run", Params: json.RawMessage(`{"name":"whoami"}`)}); out.Code != "AUTH_DENIED" {
		t.Fatal(out)
	}
	if len(r.started()) != 0 {
		t.Fatal("started")
	}
}

func TestRootRunKillsAProgramPastItsTimeout(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"slow":{"digest":"`+digestA+`","run":{"timeout_s":1}}}`)
	r.run = func(ctx context.Context, c rootCall) (int, error) {
		<-ctx.Done()
		return -1, errKilled
	}
	start := time.Now()
	if out := request(e, "root.run", map[string]any{"name": "slow"}); out.Code != "ROOT_TIMEOUT" || r.kills != 1 {
		t.Fatal(out, r.kills)
	}
	if d := time.Since(start); d < time.Second || d > 5*time.Second {
		t.Fatal(d)
	}
}

func TestRootRunKillsAProgramPastItsOutputLimit(t *testing.T) {
	e, _, r, staging := rootSetup(t, `{"loud":{"digest":"`+digestA+`","run":{"max_output":100000}}}`)
	r.run = func(ctx context.Context, c rootCall) (int, error) {
		for {
			if _, err := c.Stdout.Write(make([]byte, 4096)); err != nil {
				<-ctx.Done()
				return -1, errKilled
			}
		}
	}
	if out := request(e, "root.run", map[string]any{"name": "loud"}); out.Code != "limit-exceeded" || r.kills != 1 {
		t.Fatal(out, r.kills)
	}
	if entries, _ := os.ReadDir(staging); len(entries) != 0 {
		t.Fatal("staged output of a refused run remains", entries)
	}
}

func TestRootRunStagesOutputTooLargeToAnswerInline(t *testing.T) {
	e, _, r, staging := rootSetup(t, `{"bundle":{"digest":"`+digestA+`","run":{"max_output":67108864}},"binary":"`+digestA+`"}`)
	big := strings.Repeat("x", 1<<20)
	r.run = func(_ context.Context, c rootCall) (int, error) {
		if c.Name == "binary" {
			c.Stdout.Write([]byte{0xff, 0xfe})
			return 0, nil
		}
		io.WriteString(c.Stdout, big)
		return 0, nil
	}
	for name, want := range map[string]string{"bundle": big, "binary": "\xff\xfe"} {
		out := result(t, request(e, "root.run", map[string]any{"name": name}))
		if _, inline := out["output"]; inline || out["size"] != float64(len(want)) {
			t.Fatal(name, len(out["output"].(string)), out["size"])
		}
		staged, _ := out["staging"].(string)
		b, err := os.ReadFile(filepath.Join(staging, staged))
		if !strings.HasPrefix(staged, "root-") || err != nil || string(b) != want {
			t.Fatal(name, staged, err, len(b))
		}
	}
}

func TestRootRunStagingHasAQuota(t *testing.T) {
	e, _, r, staging := rootSetup(t, `{"bundle":{"digest":"`+digestA+`","run":{"max_output":67108864}}}`)
	if err := os.WriteFile(filepath.Join(staging, "root-old"), make([]byte, maxRootStaging-(64<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	r.run = func(ctx context.Context, c rootCall) (int, error) {
		if _, err := c.Stdout.Write(make([]byte, 1<<20)); err != nil {
			<-ctx.Done()
			return -1, errKilled
		}
		return 0, nil
	}
	if out := request(e, "root.run", map[string]any{"name": "bundle"}); out.Code != "ROOT_STAGING_FULL" {
		t.Fatal(out)
	}
}

// brokenStaging is a staging folder that cannot be opened.
type brokenStaging struct{}

func (brokenStaging) Create() (*os.File, string, error) { return nil, "", os.ErrNotExist }
func (brokenStaging) Remove(string)                     {}
func (brokenStaging) Used() (int64, error)              { return 0, os.ErrNotExist }

func TestRootRunNamesAnUnavailableStagingFolderAndLogsIt(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"bundle":{"digest":"`+digestA+`","run":{"max_output":67108864}}}`)
	e.RootStaging = brokenStaging{}
	r.run = func(ctx context.Context, c rootCall) (int, error) {
		if _, err := c.Stdout.Write(make([]byte, 1<<20)); err != nil {
			<-ctx.Done()
			return -1, errKilled
		}
		return 0, nil
	}
	var logged strings.Builder
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)
	out := request(e, "root.run", map[string]any{"name": "bundle"})
	if out.Code != "ROOT_FAILED" || out.Message != "root output staging unavailable" {
		t.Fatal(out)
	}
	if want := "root.run output staging unavailable: package=" + e.Policy.Package + " name=bundle: "; !strings.Contains(logged.String(), want) {
		t.Fatal(logged.String())
	}
}

func TestRootRunRunsTwoProgramsAtATime(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"slow":"`+digestA+`"}`)
	release := make(chan struct{})
	r.run = func(ctx context.Context, c rootCall) (int, error) { <-release; return 0, nil }
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); request(e, "root.run", map[string]any{"name": "slow"}) }()
	}
	for len(r.started()) < 2 {
		time.Sleep(time.Millisecond)
	}
	if out := request(e, "root.run", map[string]any{"name": "slow"}); out.Code != "ROOT_BUSY" {
		t.Fatal(out)
	}
	close(release)
	wg.Wait()
}

// rootConnection sends a root.run over a connection and answers its reply;
// hangUp closes the connection as soon as the request is sent.
func rootConnection(e *Executor, name string, hangUp bool) Response {
	b, _ := json.Marshal(map[string]any{"v": 1, "op": "root.run", "auth": map[string]string{"dsm_cookie": "session", "dsm_synotoken": "token"}, "params": map[string]any{"name": name}})
	a, z := net.Pipe()
	go func() {
		defer a.Close()
		e.Connection(context.Background(), a, 100, func(net.Conn) (int, error) { return 100, nil })
	}()
	z.Write(append(b, '\n'))
	if hangUp {
		z.Close()
		return Response{}
	}
	line, _ := bufio.NewReader(z).ReadBytes('\n')
	z.Close()
	var out Response
	json.Unmarshal(line, &out)
	return out
}

func (e *Executor) runningRoot() int {
	e.rootMu.Lock()
	defer e.rootMu.Unlock()
	return e.rootRunning
}

func TestRootRunFreesItsSlotBeforeItAnswers(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"report":"`+digestA+`"}`)
	r.run = func(_ context.Context, c rootCall) (int, error) {
		io.WriteString(c.Stdout, strings.Repeat("x", 100<<10))
		return 0, nil
	}
	for range 5 {
		if out := rootConnection(e, "report", false); !out.OK || e.runningRoot() != 0 {
			t.Fatal(out, e.runningRoot())
		}
	}
}

func TestRootRunKeepsTheSlotOfAClientThatHungUpUntilItsProgramEnds(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"slow":"`+digestA+`","fast":"`+digestA+`"}`)
	release := make(chan struct{})
	r.run = func(ctx context.Context, c rootCall) (int, error) {
		if c.Name == "slow" {
			<-release
		}
		return 0, nil
	}
	rootConnection(e, "slow", true)
	rootConnection(e, "slow", true)
	for len(r.started()) < 2 {
		time.Sleep(time.Millisecond)
	}
	var logged strings.Builder
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)
	if out := rootConnection(e, "fast", false); out.Code != "ROOT_BUSY" {
		t.Fatal(out)
	}
	if want := "root.run busy: package=" + e.Policy.Package + " name=fast running=2\n"; !strings.HasSuffix(logged.String(), want) {
		t.Fatal(logged.String())
	}
	close(release)
	for e.runningRoot() != 0 {
		time.Sleep(time.Millisecond)
	}
	if out := rootConnection(e, "fast", false); !out.OK {
		t.Fatal(out)
	}
}

func TestRootRunRequestsMayCarryAMebibyteOfParams(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"cli":{"digest":"`+digestA+`","schema":{"type":"object","additionalProperties":false,"properties":{"paths":{"type":"array","items":{"type":"string"}}}},"run":{}}}`)
	paths := make([]string, 1000)
	for i := range paths {
		paths[i] = "/volume1/" + strings.Repeat("p", 200)
	}
	send := func(op string, params any) Response {
		b, _ := json.Marshal(map[string]any{"v": 1, "op": op, "auth": map[string]string{"dsm_cookie": "session", "dsm_synotoken": "token"}, "params": params})
		a, z := net.Pipe()
		go func() {
			defer a.Close()
			e.Connection(context.Background(), a, 100, func(net.Conn) (int, error) { return 100, nil })
		}()
		go z.Write(append(b, '\n'))
		line, _ := bufio.NewReader(z).ReadBytes('\n')
		z.Close()
		var out Response
		json.Unmarshal(line, &out)
		return out
	}
	if out := send("root.run", map[string]any{"name": "cli", "params": map[string]any{"paths": paths}}); !out.OK || len(r.started()) != 1 {
		t.Fatal(out)
	}
	if out := send("status", map[string]any{"pad": strings.Repeat("x", 70<<10)}); out.Code != "REQUEST_TOO_LARGE" {
		t.Fatal(out)
	}
	if out := send("root.run", map[string]any{"name": "cli", "params": map[string]any{"paths": []string{strings.Repeat("x", 1<<20)}}}); out.Code != "REQUEST_TOO_LARGE" {
		t.Fatal(out)
	}
}
