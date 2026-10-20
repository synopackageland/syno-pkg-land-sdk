package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// fakeApp is the app side of the channel in Go, so the runtime is testable
// without an interpreter. handle answers one call; it may use svc.
type fakeApp struct {
	version int
	methods []string
	handle  func(c *fakeConn, call map[string]any) map[string]any
}

type fakeConn struct {
	r    *bufio.Reader
	w    net.Conn
	last []byte   // the last frame as received
	seen [][]byte // every frame received
}

func (c *fakeConn) send(v any) {
	b, _ := json.Marshal(v)
	c.w.Write(append(b, '\n'))
}

func (c *fakeConn) recv() map[string]any {
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		return nil
	}
	c.last = line
	c.seen = append(c.seen, line)
	var m map[string]any
	json.Unmarshal(line, &m)
	return m
}

func (c *fakeConn) svc(op string, params any) map[string]any {
	c.send(map[string]any{"t": "svc", "id": "s1", "op": op, "params": params})
	return c.recv()
}

func (a fakeApp) serve(conn net.Conn) {
	defer conn.Close()
	c := &fakeConn{r: bufio.NewReader(conn), w: conn}
	if c.recv() == nil {
		return
	}
	version := a.version
	if version == 0 {
		version = 1
	}
	c.send(map[string]any{"t": "hello", "version": version, "sdk": "go-test/0", "methods": a.methods})
	call := c.recv()
	if call == nil {
		return
	}
	reply := a.handle(c, call)
	if reply != nil {
		reply["t"] = "result"
		c.send(reply)
	}
}

// pipeDialer hands the runtime one end of a pipe served by app.
func pipeDialer(app fakeApp) Dialer {
	return DialerFunc(func(context.Context) (net.Conn, error) {
		runtime, appEnd := net.Pipe()
		go app.serve(appEnd)
		return runtime, nil
	})
}

var alice = Context{User: "alice", Locale: "en", RequestID: "r1"}

func TestCallReturnsTheAppData(t *testing.T) {
	app := fakeApp{methods: []string{"hello.greet"}, handle: func(_ *fakeConn, call map[string]any) map[string]any {
		ctx := call["ctx"].(map[string]any)
		return map[string]any{"ok": true, "data": map[string]any{"message": "Hello, " + ctx["user"].(string)}}
	}}
	s := &Session{Dial: pipeDialer(app), Methods: []string{"hello.greet"}}
	data, err := s.Call(context.Background(), "hello.greet", json.RawMessage(`{}`), &Call{Context: alice})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"message":"Hello, alice"}` {
		t.Fatalf("data %s", data)
	}
}

func TestAppErrorBecomesAPIError(t *testing.T) {
	app := fakeApp{methods: []string{"hello.greet"}, handle: func(*fakeConn, map[string]any) map[string]any {
		return map[string]any{"ok": false, "error": map[string]any{"code": "conflict", "message": "taken"}}
	}}
	s := &Session{Dial: pipeDialer(app), Methods: []string{"hello.greet"}}
	_, err := s.Call(context.Background(), "hello.greet", nil, &Call{Context: alice})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "conflict" || apiErr.Message != "taken" {
		t.Fatalf("err %v", err)
	}
}

func TestHandshakeRefusesAnotherVersionOrMethodSet(t *testing.T) {
	for name, app := range map[string]fakeApp{
		"version": {version: 2, methods: []string{"hello.greet"}},
		"methods": {methods: []string{"hello.greet", "hello.extra"}},
	} {
		app.handle = func(*fakeConn, map[string]any) map[string]any { return map[string]any{"ok": true, "data": 1} }
		s := &Session{Dial: pipeDialer(app), Methods: []string{"hello.greet"}}
		if _, err := s.Call(context.Background(), "hello.greet", nil, &Call{Context: alice}); !errors.Is(err, ErrProtocol) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestDeadlineSendsCancelAndReportsTimeout(t *testing.T) {
	canceled := make(chan map[string]any, 1)
	app := fakeApp{methods: []string{"slow.op"}, handle: func(c *fakeConn, _ map[string]any) map[string]any {
		canceled <- c.recv()
		return nil
	}}
	s := &Session{Dial: pipeDialer(app), Methods: []string{"slow.op"}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.Call(ctx, "slow.op", nil, &Call{Context: alice})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	select {
	case m := <-canceled:
		if m["t"] != "cancel" {
			t.Fatalf("frame %v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("the app never saw cancel")
	}
}

func TestAppAsksTheRuntimeForAServiceDuringTheCall(t *testing.T) {
	var seen Context
	services := map[string]Service{"caller.get": func(_ context.Context, c *Call, _ json.RawMessage) (any, error) {
		seen = c.Context
		return map[string]any{"user": c.User}, nil
	}}
	app := fakeApp{methods: []string{"hello.who"}, handle: func(c *fakeConn, _ map[string]any) map[string]any {
		got := c.svc("caller.get", map[string]any{})
		missing := c.svc("executor.raw", map[string]any{})
		return map[string]any{"ok": true, "data": []any{got, missing}}
	}}
	s := &Session{Dial: pipeDialer(app), Methods: []string{"hello.who"}, Services: services}
	data, err := s.Call(context.Background(), "hello.who", nil, &Call{Context: alice})
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"id":"s1","ok":true,"result":{"user":"alice"},"t":"svc_result"},{"error":{"code":"unknown_service","message":"the runtime does not offer executor.raw"},"id":"s1","ok":false,"t":"svc_result"}]`
	if string(data) != want || seen.User != "alice" {
		t.Fatalf("data %s", data)
	}
}

func TestAppCannotReturnTheRuntimeRefusals(t *testing.T) {
	for _, code := range []string{"session_required", "admin_required", "app_unavailable", "Bad Code", ""} {
		app := fakeApp{methods: []string{"hello.greet"}, handle: func(*fakeConn, map[string]any) map[string]any {
			return map[string]any{"ok": false, "error": map[string]any{"code": code, "message": "x"}}
		}}
		s := &Session{Dial: pipeDialer(app), Methods: []string{"hello.greet"}}
		if _, err := s.Call(context.Background(), "hello.greet", nil, &Call{Context: alice}); !errors.Is(err, ErrProtocol) {
			t.Fatalf("%q: %v", code, err)
		}
	}
}

func TestOversizedFrameIsAProtocolError(t *testing.T) {
	app := fakeApp{methods: []string{"big.data"}, handle: func(c *fakeConn, _ map[string]any) map[string]any {
		c.w.Write(append([]byte(`{"t":"result","ok":true,"data":"`), make([]byte, MaxFrame)...))
		return nil
	}}
	s := &Session{Dial: pipeDialer(app), Methods: []string{"big.data"}}
	if _, err := s.Call(context.Background(), "big.data", nil, &Call{Context: alice}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err %v", err)
	}
}

func isUnavailable(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr) && apiErr.Code == "app_unavailable"
}

func TestAnAppThatCannotStartIsUnavailable(t *testing.T) {
	s := &Session{Dial: DialerFunc(func(context.Context) (net.Conn, error) { return nil, errors.New("exec: no such file") }), Methods: []string{"a.b"}}
	if _, err := s.Call(context.Background(), "a.b", nil, &Call{Context: alice}); !isUnavailable(err) {
		t.Fatalf("err %v", err)
	}
}

func TestAnAppThatStopsBeforeItAnswersIsUnavailable(t *testing.T) {
	for name, dial := range map[string]Dialer{
		"before hello": DialerFunc(func(context.Context) (net.Conn, error) {
			runtime, app := net.Pipe()
			go func() { bufio.NewReader(app).ReadBytes('\n'); app.Close() }()
			return runtime, nil
		}),
		"during the call": pipeDialer(fakeApp{methods: []string{"a.b"}, handle: func(*fakeConn, map[string]any) map[string]any { return nil }}),
	} {
		s := &Session{Dial: dial, Methods: []string{"a.b"}}
		if _, err := s.Call(context.Background(), "a.b", nil, &Call{Context: alice}); !isUnavailable(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestAnAppThatNeverSaysHelloIsUnavailable(t *testing.T) {
	dial := DialerFunc(func(context.Context) (net.Conn, error) {
		runtime, app := net.Pipe()
		go io.Copy(io.Discard, app)
		return runtime, nil
	})
	s := &Session{Dial: dial, Methods: []string{"a.b"}, StartTimeout: 20 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := s.Call(ctx, "a.b", nil, &Call{Context: alice}); !isUnavailable(err) {
		t.Fatalf("err %v", err)
	}
}

// An App may return a protocol v1 refusal (spec/protocol/v1/errors.json), or
// a client code its executor service gave it (a stopped package's
// APP_STOPPED), so the browser's SDK can translate it and retry; outcome and
// reason codes are not refusals.
func TestAppMayReturnAProtocolRefusal(t *testing.T) {
	answer := func(code string) error {
		app := fakeApp{methods: []string{"a.b"}, handle: func(*fakeConn, map[string]any) map[string]any {
			return map[string]any{"ok": false, "error": map[string]any{"code": code, "message": "m"}}
		}}
		_, err := (&Session{Dial: pipeDialer(app), Methods: []string{"a.b"}}).Call(context.Background(), "a.b", nil, &Call{Context: alice})
		return err
	}
	for _, code := range []string{"SCOPE_DENIED", "PERMISSION_DENIED", "EXECUTOR_PAUSED", "EXECUTOR_UNAVAILABLE", "APP_STOPPED", "APP_STARTING"} {
		var apiErr *api.Error
		if err := answer(code); !errors.As(err, &apiErr) || apiErr.Code != code {
			t.Errorf("%s: %v", code, err)
		}
	}
	for _, code := range []string{"OPERATION_CANCELED", "SCOPE_DENIED_X", "Scope_Denied"} {
		if err := answer(code); !errors.Is(err, ErrProtocol) {
			t.Errorf("%s: %v", code, err)
		}
	}
}
