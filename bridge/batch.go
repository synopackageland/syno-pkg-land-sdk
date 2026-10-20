package bridge

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// CallBatch uses the negotiated v1 batch extension. Old Apps refuse batches
// explicitly; single-call frames and negotiation remain unchanged.
func (s *Session) CallBatch(ctx context.Context, input []api.AuthorizedCall) (out []api.BatchResult) {
	out = make([]api.BatchResult, len(input))
	fail := func(err error) []api.BatchResult {
		for i := range out {
			if !out[i].Success && out[i].Error == nil {
				out[i] = api.BatchFailure(err)
			}
		}
		return out
	}
	conn, err := s.Dial.Dial(ctx)
	if err != nil {
		return fail(AppUnavailable())
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	if d, ok := ctx.Deadline(); ok {
		conn.SetWriteDeadline(d)
	}
	frames := make(chan frame)
	errs := make(chan error, 1)
	go readFrames(conn, frames, errs, done)
	next := func(ctx context.Context) (frame, error) {
		select {
		case f := <-frames:
			return f, nil
		case err := <-errs:
			return frame{}, err
		case <-ctx.Done():
			return frame{}, ctx.Err()
		}
	}
	stop := func(err error) []api.BatchResult { return fail(s.stop(conn, next, err)) }
	if err := writeFrame(conn, map[string]any{"t": "hello", "versions": []int{Version}, "batch": 1}); err != nil {
		return stop(writeFailed(ctx, err))
	}
	hello, err := s.hello(ctx, next)
	if err != nil {
		return stop(err)
	}
	if hello.T != "hello" || hello.Version != Version || hello.Batch != 1 || !sameMethods(hello.Methods, s.Methods) {
		return fail(AppUnavailable())
	}
	calls := make([]*Call, len(input))
	wire := make([]map[string]any, len(input))
	for i, in := range input {
		c := s.newCall(in.Caller, in.Request)
		calls[i] = c
		defer c.spool.remove()
		if d, ok := ctx.Deadline(); ok {
			c.DeadlineMS = max(time.Until(d).Milliseconds(), 0)
		}
		params := in.Params
		if len(params) == 0 {
			params = json.RawMessage("null")
		}
		wire[i] = map[string]any{"t": "call", "id": strconv.Itoa(i), "method": in.Method, "params": params, "ctx": c.Context}
	}
	if err := writeFrame(conn, map[string]any{"t": "batch", "v": 1, "calls": wire}); err != nil {
		return stop(writeFailed(ctx, err))
	}
	current := 0
	for current < len(input) {
		f, err := next(ctx)
		if err != nil {
			return stop(err)
		}
		if ctx.Err() != nil {
			return stop(ctx.Err())
		}
		switch f.T {
		case "svc":
			if f.BatchID == nil || *f.BatchID != current {
				return fail(ErrProtocol)
			}
			if err := writeFrame(conn, s.serve(ctx, calls[current], f)); err != nil {
				return stop(writeFailed(ctx, err))
			}
		case "result":
			if f.ID != strconv.Itoa(current) {
				return fail(ErrProtocol)
			}
			data, err := result(f)
			if err != nil {
				out[current] = api.BatchFailure(err)
			} else {
				out[current] = api.BatchSuccess(data)
			}
			current++
		default:
			return fail(ErrProtocol)
		}
	}
	return out
}
