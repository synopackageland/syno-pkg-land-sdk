package delegate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// Script each transport boundary without depending on OS socket buffer timing.
type outcomeConn struct {
	io.Reader
	writeErr    error
	deadlineErr error
	short       bool
}

func (c *outcomeConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 1, c.writeErr
	}
	if c.short {
		return 1, nil
	}
	return len(p), nil
}
func (*outcomeConn) Close() error                     { return nil }
func (*outcomeConn) LocalAddr() net.Addr              { return nil }
func (*outcomeConn) RemoteAddr() net.Addr             { return nil }
func (c *outcomeConn) SetDeadline(time.Time) error    { return c.deadlineErr }
func (*outcomeConn) SetReadDeadline(time.Time) error  { return nil }
func (*outcomeConn) SetWriteDeadline(time.Time) error { return nil }

type outcomeReader struct{ err error }

func (r outcomeReader) Read([]byte) (int, error) { return 0, r.err }

func TestNotSentExchangeTransportBoundaries(t *testing.T) {
	broken := errors.New("connection lost")
	for _, tc := range []struct {
		name string
		conn *outcomeConn
		want bool
	}{
		{"set deadline", &outcomeConn{deadlineErr: broken}, true},
		{"partial write", &outcomeConn{writeErr: broken}, false},
		{"short write", &outcomeConn{short: true}, false},
		{"read failure", &outcomeConn{Reader: outcomeReader{broken}}, false},
		{"EOF", &outcomeConn{Reader: strings.NewReader("")}, false},
		{"timeout", &outcomeConn{Reader: outcomeReader{context.DeadlineExceeded}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			err := exchange(ctx, ctx, tc.conn, "files.write", nil, json.RawMessage(`{}`), nil, func(error) error { return fail(CodeExecutorUnavailable) })
			checkNotSent(t, err, tc.want)
		})
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := exchange(ctx, ctx, &outcomeConn{}, "files.write", nil, json.RawMessage(`{}`), nil, func(error) error { return fail(CodeExecutorUnavailable) })
	code(t, err, CodeExecutorTimeout)
	checkNotSent(t, err, true)
}

func TestNotSentExchangeRejectsInvalidAndOversizedWireBeforeWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	large, _ := json.Marshal(strings.Repeat("x", MaxFrame))
	for _, params := range []json.RawMessage{json.RawMessage("{"), large} {
		// A transport error would indicate that validation let this reach Write.
		err := exchange(ctx, ctx, &outcomeConn{writeErr: io.ErrClosedPipe}, "files.write", nil, params, nil, func(error) error { t.Error("invalid wire sent"); return fail(CodeExecutorUnavailable) })
		checkNotSent(t, err, true)
	}
}

func TestNotSentBatchStartupRefusalDoesNotCacheStatus(t *testing.T) {
	b := &batchClient{}
	c := &Client{Socket: "missing-executor.sock", Mailbox: mailbox(t, "start", 0)}
	err := b.call(context.Background(), c, "status", nil, nil, nil)
	code(t, err, CodeAppStarting)
	checkNotSent(t, err, true)
	if b.err != nil {
		t.Fatal("status poisoned later work")
	}
	c.Mailbox = mailbox(t, "stop", 0)
	err = b.call(context.Background(), c, "root.run", nil, nil, nil)
	code(t, err, CodeAppStopped)
	checkNotSent(t, err, true)
}

func TestNotSentBatchStepBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		b      *batchClient
		params any
		want   bool
	}{
		{"cached uncertain error", &batchClient{err: fail(CodeExecutorUnavailable)}, nil, true},
		{"step limit", &batchClient{count: 32}, nil, true},
		{"marshal", &batchClient{}, make(chan int), true},
		{"dial", &batchClient{}, nil, true},
		{"auth mismatch", &batchClient{conn: &outcomeConn{}, auth: map[string]string{"dsm_cookie": "other"}}, nil, true},
		{"oversize", &batchClient{conn: &outcomeConn{}}, strings.Repeat("x", MaxRootRequest), true},
		{"partial write", &batchClient{conn: &outcomeConn{writeErr: io.ErrClosedPipe}}, nil, false},
		{"short write", &batchClient{conn: &outcomeConn{short: true}}, nil, false},
		{"read EOF", &batchClient{conn: &outcomeConn{}, reader: bufio.NewReader(strings.NewReader(""))}, nil, false},
		{"read transport", &batchClient{conn: &outcomeConn{}, reader: bufio.NewReader(outcomeReader{io.ErrClosedPipe})}, nil, false},
		{"read timeout", &batchClient{conn: &outcomeConn{}, reader: bufio.NewReader(outcomeReader{context.DeadlineExceeded})}, nil, false},
		{"malformed", &batchClient{conn: &outcomeConn{}, reader: bufio.NewReader(strings.NewReader("{}\n"))}, nil, false},
		{"oversize reply", &batchClient{conn: &outcomeConn{}, reader: bufio.NewReader(strings.NewReader(strings.Repeat("x", MaxFrame+1)))}, nil, false},
		{"policy refusal", &batchClient{conn: &outcomeConn{}, reader: bufio.NewReader(strings.NewReader("{\"ok\":false,\"code\":\"POLICY_DENIED\"}\n"))}, nil, true},
		{"uncertain refusal", &batchClient{conn: &outcomeConn{}, reader: bufio.NewReader(strings.NewReader("{\"ok\":false,\"code\":\"ROOT_TIMEOUT\"}\n"))}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{Socket: "missing-executor.sock"}
			err := tc.b.call(context.Background(), c, "root.run", nil, tc.params, nil)
			if tc.name == "dial" {
				var dial *net.OpError
				if !NotSent(err) || !errors.As(err, &dial) {
					t.Fatalf("lost dial evidence: %v", err)
				}
				return
			}
			checkNotSent(t, err, tc.want)
		})
	}
}

func TestNotSentBatchOpenFailureAndLaterUnsentStep(t *testing.T) {
	c := executor(t, func(Request) string { return "" })
	b := &batchClient{}
	err := b.call(context.Background(), c, "root.run", nil, nil, nil)
	checkNotSent(t, err, true) // Lost root.batch reply, no business step sent.
	if b.conn != nil {
		defer b.conn.Close()
	}
	if b.stop != nil {
		defer b.stop()
	}
	checkNotSent(t, b.call(context.Background(), c, "root.run", nil, nil, nil), true)
}

func TestNotSentBatchCancellationAfterStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &batchClient{conn: &outcomeConn{}, reader: bufio.NewReader(outcomeReader{io.ErrClosedPipe})}
	err := b.call(ctx, &Client{}, "root.run", nil, nil, nil)
	code(t, err, "canceled")
	checkNotSent(t, err, false)
	// The next call inherits the terminal stream error, but its own step never ran.
	checkNotSent(t, b.call(ctx, &Client{}, "root.run", nil, nil, nil), true)
}
