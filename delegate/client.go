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
	"github.com/synopackageland/syno-pkg-land-sdk/internal/wirelimits"
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

// MaxWebAPIReply bounds compact JSON; MaxReplyFrame includes envelope overhead.
const MaxWebAPIReply = wirelimits.MaxWebAPIReply
const MaxReplyFrame = wirelimits.MaxReplyFrame

func replyLimit(op string) int {
	if op == "webapi.call" {
		return MaxReplyFrame
	}
	return MaxFrame
}

// ExecutorTimeout is the default wait, except for root.run and raw WebAPI:
// the executor uses the shared operation allowance; the client waits 10 longer
// (spec/protocol/v1/timeouts.json).
const ExecutorTimeout = runtimebudget.Operation + 10*time.Second

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
	// WorkBudget optionally bounds each Call after local admission. Queueing
	// uses only the caller context; earlier caller deadlines still win. The
	// budget includes busy backoff and all per-hop reply reserves. Values <= 0
	// preserve the existing behavior. Budgeted calls use fresh connections,
	// including within an API batch, to give each call its own wire deadline.
	WorkBudget time.Duration
	// Mailbox is the package's lifecycle mailbox (<data dir>/executor). With
	// it, a socket that does not answer is APP_STOPPED when the package is
	// stopped. Work right after a start waits for the executor; status probes
	// report APP_STARTING immediately when it is not listening yet.
	Mailbox string
}

// StartGrace is how long after a package start a client waits for its
// executor to listen: the executor reads the mailbox every half second.
const StartGrace = runtimebudget.Operation

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

// Call shares root/WebAPI admission with clients for the same socket in this
// process. Only an explicit pre-execution ROOT_BUSY is retried; queueing and
// backoff honor the caller context without adding an elapsed wait limit.
func (c *Client) Call(parent context.Context, op string, auth map[string]string, params any, result any) error {
	if parent.Err() != nil {
		return notSent(fail("canceled"))
	}
	if c == nil || c.Socket == "" {
		return notSent(fail(CodeExecutorUnavailable))
	}
	if needsRootSlot(op) {
		release, err := acquireAdmission(parent, c.Socket)
		if err != nil {
			return err
		}
		defer release()
	}
	if c.WorkBudget > 0 {
		var cancel context.CancelFunc
		parent, cancel = context.WithTimeout(parent, c.WorkBudget)
		defer cancel()
	}
	var stream *batchClient
	if c.WorkBudget <= 0 && (isBatchRootOp(op) || op == "status" && auth["dsm_cookie"] != "") {
		stream = requestBatchClient(parent, c)
	}
	delay := 100 * time.Millisecond
	for {
		if parent.Err() != nil {
			return notSent(fail("canceled"))
		}
		var err error
		if stream != nil {
			err = stream.call(parent, c, op, auth, params, result)
			// A refused batch step may be repeated as a fresh authenticated request.
			// Do not consume the batch's bounded step count with repeated refusals.
			stream = nil
		} else {
			err = c.callOnce(parent, op, auth, params, result)
		}
		if !needsRootSlot(op) || !rootBusy(err) {
			return err
		}
		if err := waitRootBusy(parent, delay); err != nil {
			return err
		}
		delay = min(2*delay, time.Second)
	}
}

// callOnce uses one connection per attempt. Refusals survive unchanged; sent
// requests with lost replies are terminal and are never automatically replayed.
func (c *Client) callOnce(parent context.Context, op string, auth map[string]string, params any, result any) (err error) {
	exchanging := false
	defer func() {
		if !exchanging {
			err = notSent(err)
		}
	}()
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
	conn, e := c.dial(ctx, op != "status")
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
	exchanging = true
	return exchange(parent, ctx, conn, op, auth, p, result, transport)
}

// exchange marks the send boundary conservatively: a failed/partial Write may
// have delivered a complete request, so only earlier errors prove no execution.
func exchange(parent, ctx context.Context, conn net.Conn, op string, auth map[string]string, p json.RawMessage, result any, transport func(error) error) (err error) {
	sent := false
	defer func() {
		if !sent {
			err = notSent(err)
		}
	}()
	deadline, _ := ctx.Deadline()
	if e := conn.SetDeadline(deadline); e != nil {
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
	sent = true
	if _, e = io.Copy(conn, bytes.NewReader(b)); e != nil {
		return transport(e)
	}
	limit := replyLimit(op)
	line, e := bufio.NewReader(io.LimitReader(conn, int64(limit)+1)).ReadBytes('\n')
	if len(line) > limit {
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
		if reply.Code == CodeWebapiReplyTooLarge {
			return &api.Error{Code: reply.Code, Message: reply.Message}
		}
		err := fail(reply.Code)
		if preAdmissionCodes[reply.Code] {
			return notSent(err)
		}
		return err
	}
	if len(reply.Result) == 0 || string(reply.Result) == "null" || reply.Result[0] != '{' {
		return fail(CodeExecutorProtocol)
	}
	if raw, ok := result.(*json.RawMessage); ok {
		*raw = reply.Result
		return nil
	}
	if result != nil && json.Unmarshal(reply.Result, result) != nil {
		return fail(CodeExecutorProtocol)
	}
	return nil
}

// dial connects to the executor. A stopped package is APP_STOPPED at once,
// before a dial: while it stops, its executor can hold the socket without
// answering. Work after a recent start waits for the executor to listen, and
// ends APP_STARTING. Status only probes once, reporting APP_STARTING from the
// recent mailbox request without waiting or changing the work allowance.
// A protected policy comparison
// indicating pending approval returns EXECUTOR_UNAVAILABLE without waiting
// for a socket that cannot open until approval. Neither sent a byte.
func (c *Client) dial(ctx context.Context, waitForStart bool) (net.Conn, error) {
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
		case approvalPending(c):
			return nil, fail(CodeExecutorUnavailable)
		case action != "start":
			return nil, err
		case !waitForStart && left > 0:
			return nil, fail(CodeAppStarting)
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
