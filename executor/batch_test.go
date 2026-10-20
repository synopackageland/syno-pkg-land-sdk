package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

func TestBatchConnectionVerifiesOnceAndRechecksPrivileges(t *testing.T) {
	e, f, r, _ := rootSetup(t, `{"whoami":{"digest":"`+digestA+`","kind":"binary","run":{}}}`)
	a, b := net.Pipe()
	defer b.Close()
	go func() {
		defer a.Close()
		e.Connection(context.Background(), a, 1200, func(net.Conn) (int, error) { return 1200, nil })
	}()
	enc, dec := json.NewEncoder(b), json.NewDecoder(bufio.NewReader(b))
	send := func(req Request) Response {
		t.Helper()
		if err := enc.Encode(req); err != nil {
			t.Fatal(err)
		}
		var out Response
		if err := dec.Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	opened := send(Request{Version: 1, Op: "root.batch", Auth: Auth{Cookie: "session", SynoToken: "token", RemoteAddr: "192.0.2.1"}, Params: json.RawMessage(`{"v":1,"timeout_ms":5000}`)})
	if !opened.OK {
		t.Fatalf("batch open: %+v", opened)
	}
	req := Request{Version: 1, Op: "root.run", Params: json.RawMessage(`{"name":"whoami"}`)}
	if out := send(req); !out.OK {
		t.Fatal(out)
	}
	if out := send(Request{Version: 1, Op: "status", Params: json.RawMessage(`{}`)}); !out.OK {
		t.Fatal("authenticated status in scope", out)
	}
	req.Params = json.RawMessage(`{"name":"missing"}`)
	if out := send(req); out.Code != "POLICY_DENIED" {
		t.Fatal(out)
	}
	f.privilegeErr = errors.New("revoked")
	req.Params = json.RawMessage(`{"name":"whoami"}`)
	if out := send(req); out.Code != "APP_DENIED" {
		t.Fatal(out)
	}
	if f.accountCalls != 1 || len(r.started()) != 1 {
		t.Fatalf("lookups=%d root calls=%d", f.accountCalls, len(r.started()))
	}
}

func TestBatchDisconnectCancelsActiveRootRun(t *testing.T) {
	e, _, runner, _ := rootSetup(t, `{"slow":{"digest":"`+digestA+`","kind":"binary","run":{}}}`)
	started := make(chan struct{})
	runner.run = func(ctx context.Context, _ rootCall) (int, error) { close(started); <-ctx.Done(); return -1, ctx.Err() }
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		e.Connection(context.Background(), server, 1200, func(net.Conn) (int, error) { return 1200, nil })
	}()
	encode, decode := json.NewEncoder(client), json.NewDecoder(client)
	encode.Encode(Request{Version: 1, Op: "root.batch", Auth: Auth{Cookie: "session", SynoToken: "token", RemoteAddr: "192.0.2.1"}, Params: json.RawMessage(`{"v":1,"timeout_ms":5000}`)})
	var opened Response
	if err := decode.Decode(&opened); err != nil || !opened.OK {
		client.Close()
		t.Fatal(opened, err)
	}
	encode.Encode(Request{Version: 1, Op: "root.run", Params: json.RawMessage(`{"name":"slow"}`)})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		client.Close()
		t.Fatal("root did not start")
	}
	client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not end batch")
	}
	if runner.kills != 1 {
		t.Fatalf("root kills=%d", runner.kills)
	}
}
