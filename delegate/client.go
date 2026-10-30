// Package delegate provides the fixed v1 executor protocol. Credentials exist
// only in a live request; workers carry a delegation and job binding instead.
package delegate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxFrame = 64 * 1024

// ExecutorTimeout is the default wait, except for root.run and raw WebAPI:
// the executor answers within 30 seconds, and the client waits 10 longer
// (spec/protocol/v1/timeouts.json).
const ExecutorTimeout = 40 * time.Second

var stableCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,127}$`)

type Request struct {
	DeadlineMS int64             `json:"deadline_ms,omitempty"`
	V          int               `json:"v"`
	Op         string            `json:"op"`
	Auth       map[string]string `json:"auth,omitempty"`
	Params     json.RawMessage   `json:"params"`
}
type Client struct {
	Socket  string
	Timeout time.Duration
	// Mailbox is the package's lifecycle mailbox (<data dir>/executor). With
	// it, a socket that does not answer is APP_STOPPED when the package is
	// stopped, and a call right after a start waits for the executor.
	Mailbox string
}

// StartGrace is how long after a package start a client waits for its
// executor to listen: the executor reads the mailbox every half second.
const StartGrace = 10 * time.Second

// ExecutorClient keeps the session/staging bridge independent of transport.
// The production client still uses the existing bounded v1 Unix socket.
type ExecutorClient interface {
	Call(context.Context, string, map[string]string, any, any) error
}

var _ ExecutorClient = (*Client)(nil)

// Local is the package's executor; it reads the package's lifecycle mailbox
// (Mailbox), so a stopped or starting package answers APP_STOPPED or
// APP_STARTING.
func Local(packageID string) *Client {
	return &Client{
		Socket:  "/run/synopkgland/" + packageID + "/executor.sock",
		Timeout: ExecutorTimeout,
		Mailbox: "/var/packages/" + packageID + "/var/executor",
	}
}

// requestLimit is the request line an op may send: root programs take params
// of up to a mebibyte.
func requestLimit(op string) int {
	if op == "root.run" || op == "root.start" {
		return MaxRootRequest
	}
	return MaxFrame
}

// timeoutFor lets root.run wait for its program.
func timeoutFor(op string, timeout time.Duration) time.Duration {
	if op == "root.run" {
		return max(timeout, RootRunTimeout)
	}
	return timeout
}

func fail(code string) error {
	if code == CodeNoSpace {
		return &api.Error{Code: code, Message: "Insufficient available space on the destination filesystem"}
	}
	return &api.Error{Code: code, Message: "Executor operation refused (" + code + ")"}
}

// dialFailure means no connection was established and no request bytes were
// sent. Unwrap preserves the public API code without exposing socket details.
type dialFailure struct{ error }

func (e *dialFailure) Unwrap() error { return e.error }

// Call uses one connection per request, including its newline in the size
// budget. Refusal codes survive unchanged; executor messages never expose
// cookie values or internal paths through the CGI response.
func (c *Client) Call(parent context.Context, op string, auth map[string]string, params any, result any) error {
	if parent.Err() != nil {
		return fail("canceled")
	}
	if isBatchRootOp(op) || op == "status" && auth["dsm_cookie"] != "" {
		if stream := requestBatchClient(parent, c); stream != nil {
			return stream.call(parent, c, op, auth, params, result)
		}
	}
	if c == nil || c.Socket == "" {
		return fail(CodeExecutorUnavailable)
	}
	p, e := json.Marshal(params)
	if e != nil {
		return fail(CodeInvalidRequest)
	}
	if params == nil {
		p = []byte("{}")
	}
	preflight, e := json.Marshal(Request{V: 1, Op: op, Auth: auth, Params: p})
	if e != nil {
		return fail(CodeInvalidRequest)
	}
	if len(preflight)+1 > requestLimit(op) {
		return fail(CodeExecutorTooLarge)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = ExecutorTimeout
	}
	timeout = timeoutFor(op, timeout)
	rawWebAPI := false
	if op == "webapi.call" && (c.Timeout <= 0 || c.Timeout == ExecutorTimeout) {
		var mode struct {
			ResponseType string `json:"response_type"`
			ExpectedSize *int64 `json:"expected_size"`
		}
		if json.Unmarshal(p, &mode) == nil && mode.ResponseType == "raw" {
			rawWebAPI = true
			timeout = WebAPIRawTimeout + 20*time.Second
			if mode.ExpectedSize != nil {
				timeout = WebAPIRawTimeoutForSize(*mode.ExpectedSize) + 20*time.Second
			}
		}
	}
	if until, ok := parent.Deadline(); ok {
		remaining := runtimebudget.Inner(time.Until(until))
		if !rawWebAPI && op != "root.run" && (c.Timeout <= 0 || c.Timeout == ExecutorTimeout) {
			timeout = remaining
		} else {
			timeout = min(timeout, remaining)
		}
	}
	if timeout <= 0 {
		return fail(CodeExecutorTimeout)
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	transport := func(err error) error {
		if parent.Err() != nil {
			return fail("canceled")
		}
		if ctx.Err() != nil {
			return fail(CodeExecutorTimeout)
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return fail(CodeExecutorTimeout)
		}
		return fail(CodeExecutorUnavailable)
	}
	conn, e := c.dial(ctx)
	if e != nil {
		if parent.Err() != nil {
			return fail("canceled")
		}
		var refusal *api.Error
		if errors.As(e, &refusal) {
			return &dialFailure{e}
		}
		return &dialFailure{transport(e)}
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if e = conn.SetDeadline(deadline); e != nil {
		return transport(e)
	}
	remaining := runtimebudget.Inner(time.Until(deadline))
	if remaining <= 0 {
		return fail(CodeExecutorTimeout)
	}
	deadlineMS := int64(0)
	if _, ok := parent.Deadline(); ok {
		deadlineMS = remaining.Milliseconds()
	}
	b, e := json.Marshal(Request{V: 1, Op: op, Auth: auth, Params: p, DeadlineMS: deadlineMS})
	if e != nil {
		return fail(CodeInvalidRequest)
	}
	b = append(b, '\n')
	if len(b) > requestLimit(op) {
		return fail(CodeExecutorTooLarge)
	}
	if _, e = io.Copy(conn, bytes.NewReader(b)); e != nil {
		return transport(e)
	}
	line, e := bufio.NewReader(io.LimitReader(conn, MaxFrame+1)).ReadBytes('\n')
	if len(line) > MaxFrame {
		return fail(CodeExecutorTooLarge)
	}
	if e != nil {
		if !errors.Is(e, io.EOF) {
			return transport(e)
		}
		return fail(CodeExecutorProtocol)
	}
	return decodeReply(line, result)
}

func decodeReply(line []byte, result any) error {
	if !utf8.Valid(line) {
		return fail(CodeExecutorProtocol)
	}
	var reply struct {
		OK      *bool           `json:"ok"`
		Result  json.RawMessage `json:"result"`
		Code    string          `json:"code"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(line, &reply) != nil || reply.OK == nil {
		return fail(CodeExecutorProtocol)
	}
	if !*reply.OK {
		if !stableCode.MatchString(reply.Code) {
			return fail(CodeExecutorProtocol)
		}
		return fail(reply.Code)
	}
	if len(reply.Result) == 0 || string(reply.Result) == "null" || reply.Result[0] != '{' {
		return fail(CodeExecutorProtocol)
	}
	if result != nil && json.Unmarshal(reply.Result, result) != nil {
		return fail(CodeExecutorProtocol)
	}
	return nil
}

// dial connects to the executor. A stopped package is APP_STOPPED at once,
// before a dial: while it stops, its executor can hold the socket without
// answering. A package start requested less than StartGrace ago waits for the
// executor to listen, and ends APP_STARTING. Neither sent a byte.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	if action, _ := c.lifecycle(); action == "stop" {
		return nil, fail(CodeAppStopped)
	}
	for waited := false; ; waited = true {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
		if err == nil {
			return conn, nil
		}
		action, at := c.lifecycle()
		left := StartGrace - time.Since(at)
		switch {
		case action == "stop":
			return nil, fail(CodeAppStopped)
		case action != "start":
			return nil, err
		case left <= 0 && waited:
			return nil, fail(CodeAppStarting)
		case left <= 0:
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(min(100*time.Millisecond, left)):
		}
	}
}

// lifecycle is the mailbox's last request and when it was written.
func (c *Client) lifecycle() (string, time.Time) {
	if c.Mailbox == "" {
		return "", time.Time{}
	}
	return lifecycleRequest(c.Mailbox)
}

// lifecycleRequest is the last request in the lifecycle mailbox dir and when
// it was written; none reads as "".
func lifecycleRequest(dir string) (string, time.Time) {
	f, err := os.Open(filepath.Join(dir, "control"))
	if err != nil {
		return "", time.Time{}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", time.Time{}
	}
	b := make([]byte, 16)
	n, _ := f.Read(b)
	return strings.TrimSpace(string(b[:n])), info.ModTime()
}
