// Package delegatetest serves a fake package executor on a Unix socket, for
// an App's end-to-end tests: pass Executor.Socket as bridge.Config's
// ExecutorSocket (or a delegate.Client's Socket) and the runtime's root.* and
// delegate.* calls reach reply instead of the executor at
// /run/synopkgland/<package>/executor.sock, without root and on macOS. It
// speaks protocol v1's framing only (spec/protocol/v1): it verifies no
// session and enforces no policy.
package delegatetest

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/delegate"
)

// Executor is a running fake executor; the test's cleanup stops it.
type Executor struct {
	// Socket is the executor's Unix socket.
	Socket string

	mu       sync.Mutex
	requests []delegate.Request
	reply    func(delegate.Request) string
}

// NewExecutor serves reply's line for each request; nil refuses every
// operation with UNKNOWN_OP. The socket is in a new directory of the
// system's temporary directory, short enough for every Unix.
func NewExecutor(t testing.TB, reply func(delegate.Request) string) *Executor {
	t.Helper()
	if reply == nil {
		reply = func(delegate.Request) string { return Refused(delegate.CodeUnknownOp) }
	}
	dir, err := os.MkdirTemp("", "executor-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	e := &Executor{Socket: filepath.Join(dir, "executor.sock"), reply: reply}
	l, err := net.Listen("unix", e.Socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go e.serve(conn)
		}
	}()
	return e
}

// serve answers one request line, as the executor does.
func (e *Executor) serve(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	var q delegate.Request
	if err != nil || json.Unmarshal(line, &q) != nil {
		return
	}
	e.mu.Lock()
	e.requests = append(e.requests, q)
	e.mu.Unlock()
	conn.Write([]byte(e.reply(q) + "\n"))
}

// Requests are the requests the executor received, in order.
func (e *Executor) Requests() []delegate.Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]delegate.Request(nil), e.requests...)
}

// OK is the reply line of a success with result, a JSON object.
func OK(result string) string { return `{"ok":true,"result":` + result + `}` }

// Refused is the reply line of a refusal with code.
func Refused(code string) string {
	return `{"ok":false,"code":"` + code + `","message":"refused (` + code + `)"}`
}
