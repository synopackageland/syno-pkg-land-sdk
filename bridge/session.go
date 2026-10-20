// Package bridge hands a request the api package already verified to an App
// written in another language, over the channel in spec/bridge/v1. Origin,
// DSM session, entry gate, route policy and deadline stay in Go: the App
// only sees calls that passed them, and asks back for the services in
// Services, never for the session itself.
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/jsonlimits"
)

// Version is the channel protocol version this runtime speaks.
const Version = 1

// MaxFrame bounds one frame in either direction, newline included: the
// largest JSON request body (api.MaxRequestBytesCeiling) plus the envelope.
const MaxFrame = api.MaxRequestBytesCeiling + 64<<10

// CancelGrace is how long the App may take to stop after cancel.
const CancelGrace = time.Second

// DefaultStartTimeout bounds the wait for the App's hello when Session sets
// none. A cold Node start on an ARM NAS took up to 1.2 seconds.
const DefaultStartTimeout = 10 * time.Second

// ErrProtocol means the App broke the channel protocol; the browser sees
// only "internal".
var ErrProtocol = errors.New("bridge: app broke the channel protocol")

// errGone means the channel ended before the App answered: it did not start,
// crashed or exited.
var errGone = errors.New("bridge: the App closed the channel")

// AppUnavailable is the browser's error when the App could not start, stopped
// before it answered, or did not say hello within the start timeout. A
// request deadline that ends first stays request_timeout, as for a Go App.
func AppUnavailable() *api.Error {
	return &api.Error{Code: "app_unavailable", Message: "The App is not available right now. Try again later."}
}

func unavailable(cause error) error {
	log.Printf("bridge: app unavailable: %v", cause)
	return AppUnavailable()
}

// Dialer opens one channel to the App: in v1 a fresh process per call (Exec).
type Dialer interface {
	Dial(ctx context.Context) (net.Conn, error)
}

// DialerFunc adapts a function to Dialer.
type DialerFunc func(ctx context.Context) (net.Conn, error)

func (f DialerFunc) Dial(ctx context.Context) (net.Conn, error) { return f(ctx) }

// Context is what the App learns about a call. The runtime fills it from the
// verified api.Caller and the request; the App cannot change it.
type Context struct {
	User       string `json:"user"`
	Admin      bool   `json:"admin"`
	Locale     string `json:"locale"`
	RequestID  string `json:"request_id"`
	DeadlineMS int64  `json:"deadline_ms"`
}

// Call is one verified request on its way to the App. The App sees only
// Context; the caller and the request, which carries the DSM session, stay in
// the runtime for the services to act with.
type Call struct {
	Context
	Caller  api.Caller
	Request *http.Request
	spool   *spool
}

// Service is one thing the App may ask the runtime to do during a call.
type Service func(ctx context.Context, call *Call, params json.RawMessage) (any, error)

// Session runs calls against one App.
type Session struct {
	Dial     Dialer
	Methods  []string           // the App's methods, from the trusted manifest
	Services map[string]Service // what the App may ask for; nothing else
	// StartTimeout bounds the wait for the App's hello; 0 is DefaultStartTimeout.
	StartTimeout time.Duration
	// Transfers are the methods that are an upload or a download, from the
	// manifest; Spool is the directory for their files (SpoolDir).
	Transfers map[string]string
	Spool     string
	// MaxUploadBytes bounds an upload; 0 is MaxUploadBytes.
	MaxUploadBytes int64
}

type frame struct {
	Batch   int             `json:"batch,omitempty"`
	BatchID *int            `json:"batch_id,omitempty"`
	T       string          `json:"t"`
	ID      string          `json:"id,omitempty"`
	Version int             `json:"version,omitempty"`
	SDK     string          `json:"sdk,omitempty"`
	Methods []string        `json:"methods,omitempty"`
	OK      *bool           `json:"ok,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   *appError       `json:"error,omitempty"`
	Op      string          `json:"op,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type appError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Call runs method in the App and returns its data as raw JSON.
func (s *Session) Call(ctx context.Context, method string, params json.RawMessage, call *Call) (json.RawMessage, error) {
	if len(params) == 0 {
		params = json.RawMessage("null")
	}
	if d, ok := ctx.Deadline(); ok {
		call.DeadlineMS = max(time.Until(d).Milliseconds(), 0)
	}
	timeout := s.StartTimeout
	if timeout <= 0 {
		timeout = DefaultStartTimeout
	}
	startup, cancelStartup := context.WithTimeout(ctx, timeout)
	defer cancelStartup()
	until, _ := startup.Deadline()
	conn, err := s.Dial.Dial(context.WithValue(ctx, startupDeadlineKey{}, until))
	if err != nil {
		return nil, unavailable(err)
	}
	done := make(chan struct{})
	defer func() { close(done); conn.Close() }()
	// An App that stops reading must not hold a write past the deadline;
	// api.Server gives every request one.
	if d, ok := ctx.Deadline(); ok {
		conn.SetWriteDeadline(d)
	}
	frames := make(chan frame)
	readErr := make(chan error, 1)
	go readFrames(conn, frames, readErr, done)
	next := func(ctx context.Context) (frame, error) {
		select {
		case f := <-frames:
			return f, nil
		case err := <-readErr:
			return frame{}, err
		case <-ctx.Done():
			return frame{}, ctx.Err()
		}
	}

	if err := writeFrame(conn, map[string]any{"t": "hello", "versions": []int{Version}}); err != nil {
		return nil, s.stop(conn, next, writeFailed(ctx, err))
	}
	hello, err := s.hello(startup, next)
	if err != nil && ctx.Err() == nil && startup.Err() != nil {
		err = fmt.Errorf("%w: startup budget exhausted", errGone)
	}
	if err != nil {
		return nil, s.stop(conn, next, err)
	}
	if hello.T != "hello" || hello.Version != Version || !sameMethods(hello.Methods, s.Methods) {
		return nil, fmt.Errorf("%w: hello %+v", ErrProtocol, hello)
	}
	if err := writeFrame(conn, map[string]any{"t": "call", "id": "1", "method": method, "params": params, "ctx": call.Context}); err != nil {
		return nil, s.stop(conn, next, writeFailed(ctx, err))
	}
	for {
		f, err := next(ctx)
		if err != nil {
			return nil, s.stop(conn, next, err)
		}
		switch f.T {
		case "result":
			return result(f)
		case "svc":
			if err := writeFrame(conn, s.serve(ctx, call, f)); err != nil {
				return nil, s.stop(conn, next, writeFailed(ctx, err))
			}
		default:
			return nil, fmt.Errorf("%w: unexpected %q", ErrProtocol, f.T)
		}
	}
}

// writeFailed is why a write failed: the request ended (the write deadline is
// the request's, and may pass before the context's timer fires), or the App
// is gone.
func writeFailed(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return fmt.Errorf("%w: %v", errGone, err)
}

// hello waits for the App's hello, at most StartTimeout.
func (s *Session) hello(ctx context.Context, next func(context.Context) (frame, error)) (frame, error) {
	timeout := s.StartTimeout
	if timeout <= 0 {
		timeout = DefaultStartTimeout
	}
	start, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	f, err := next(start)
	if err != nil && ctx.Err() == nil && start.Err() != nil {
		return frame{}, fmt.Errorf("%w: no hello within %v", errGone, timeout)
	}
	return f, err
}

// stop reports an App that went away as unavailable. When the context ended,
// it tells the App the call is over and gives it CancelGrace to wind down
// before the channel closes.
func (s *Session) stop(conn net.Conn, next func(context.Context) (frame, error), err error) error {
	if errors.Is(err, errGone) {
		return unavailable(err)
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return err
	}
	reason := "deadline"
	if errors.Is(err, context.Canceled) {
		reason = "canceled"
	}
	conn.SetWriteDeadline(time.Now().Add(CancelGrace))
	if writeFrame(conn, map[string]any{"t": "cancel", "id": "1", "reason": reason}) == nil {
		grace, cancel := context.WithTimeout(context.Background(), CancelGrace)
		defer cancel()
		next(grace)
	}
	return err
}

// MaxServiceParams bounds the params of one svc; a service needs paths and
// names, never file contents, which go through the spool.
const MaxServiceParams = 64 << 10

// MaxRootServiceParams bounds root.run and root.start, whose program params
// may fill the executor's 1 MiB request (spec/protocol/v1, Transport).
const MaxRootServiceParams = 1 << 20

func serviceParamsLimit(op string) int {
	if op == "root.run" || op == "root.start" {
		return MaxRootServiceParams
	}
	return MaxServiceParams
}

func (s *Session) serve(ctx context.Context, call *Call, f frame) map[string]any {
	reply := map[string]any{"t": "svc_result", "id": f.ID}
	out, err := s.run(ctx, call, f)
	if err != nil {
		var apiErr *api.Error
		if !errors.As(err, &apiErr) {
			log.Printf("bridge: service %s: %v", f.Op, err)
			apiErr = &api.Error{Code: "internal", Message: "internal error"}
		}
		reply["ok"], reply["error"] = false, appError{serviceCode(apiErr.Code), apiErr.Message}
		return reply
	}
	reply["ok"], reply["result"] = true, out
	return reply
}

func (s *Session) run(ctx context.Context, call *Call, f frame) (out any, err error) {
	service, ok := s.Services[f.Op]
	if !ok {
		return nil, &api.Error{Code: "unknown_service", Message: "the runtime does not offer " + f.Op}
	}
	if len(f.Params) > serviceParamsLimit(f.Op) {
		return nil, &api.Error{Code: "request_too_large", Message: "svc params too large"}
	}
	defer func() {
		if p := recover(); p != nil {
			out, err = nil, fmt.Errorf("panic: %v", p)
		}
	}()
	return service(ctx, call, f.Params)
}

// serviceCode fits a Go SDK code to the svc frame's code pattern: some
// packages spell codes with hyphens (invalid-request), the frame has none.
func serviceCode(code string) string {
	return strings.ReplaceAll(code, "-", "_")
}

var errorCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// reservedCodes are the runtime's own refusals. An App that returns one
// would make the browser act on a decision the runtime did not take (sign in
// again, for instance), so it is reported as internal instead.
var reservedCodes = map[string]bool{
	"session_required": true, "admin_required": true, "app_privilege_denied": true,
	"auth_unavailable": true, "unknown_method": true, "cross_origin": true,
	"origin_required": true, "request_timeout": true, "request_canceled": true,
	"request_too_large": true, "json_required": true, "method_not_allowed": true,
	"app_unavailable": true,
}

// appCode is an error code an App may return: its own lower-case code, or a
// protocol v1 refusal it got from a service and passes on.
func appCode(code string) bool {
	return errorCode.MatchString(code) && !reservedCodes[code] || protocolRefusals[code]
}

func result(f frame) (json.RawMessage, error) {
	if f.OK == nil {
		return nil, fmt.Errorf("%w: result without ok", ErrProtocol)
	}
	if !*f.OK {
		if f.Error == nil || !appCode(f.Error.Code) || len(f.Error.Message) > 1024 {
			return nil, fmt.Errorf("%w: error %+v", ErrProtocol, f.Error)
		}
		return nil, &api.Error{Code: f.Error.Code, Message: f.Error.Message}
	}
	if len(f.Data) == 0 {
		return json.RawMessage("null"), nil
	}
	if !jsonlimits.WithinLimits(f.Data) {
		return nil, fmt.Errorf("%w: data beyond JSON limits", ErrProtocol)
	}
	return f.Data, nil
}

func sameMethods(got, want []string) bool {
	got, want = slices.Clone(got), slices.Clone(want)
	slices.Sort(got)
	slices.Sort(want)
	return slices.Equal(got, want)
}

func writeFrame(conn net.Conn, v any) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := conn.Write(b.Bytes())
	return err
}

func readFrames(conn net.Conn, out chan<- frame, errs chan<- error, done <-chan struct{}) {
	r := bufio.NewReaderSize(conn, 64<<10)
	for {
		line, err := readLine(r)
		if err != nil {
			errs <- err
			return
		}
		var f frame
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil || dec.More() {
			errs <- fmt.Errorf("%w: bad frame", ErrProtocol)
			return
		}
		select {
		case out <- f:
		case <-done:
			return
		}
	}
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > MaxFrame {
			return nil, fmt.Errorf("%w: frame over %d bytes", ErrProtocol, MaxFrame)
		}
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, fmt.Errorf("%w: %v", errGone, err)
		}
	}
}

type startupDeadlineKey struct{}
