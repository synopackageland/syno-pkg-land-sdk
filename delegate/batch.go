package delegate

import (
	"bufio"
	"context"
	"encoding/json"
	"maps"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type batchClientKey struct{ socket, mailbox string }
type batchClient struct {
	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
	auth   map[string]string
	err    error
	count  int
	stop   func() bool
}

func isBatchRootOp(op string) bool {
	return op == "root.run" || op == "root.start" || op == "root.jobs.list" || op == "root.jobs.get" || op == "root.jobs.stop"
}
func requestBatchClient(ctx context.Context, c *Client) *batchClient {
	if c == nil {
		return nil
	}
	value := api.BatchResource(ctx, batchClientKey{c.Socket, c.Mailbox}, func() (any, func()) {
		b := &batchClient{}
		return b, func() {
			if b.stop != nil {
				b.stop()
			}
			if b.conn != nil {
				b.conn.Close()
			}
		}

	})
	if value == nil {
		return nil
	}
	return value.(*batchClient)
}
func (b *batchClient) call(ctx context.Context, c *Client, op string, auth map[string]string, params, result any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	if b.count >= 32 {
		return fail(CodeInvalidRequest)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fail(CodeInvalidRequest)
	}
	if params == nil {
		raw = []byte("{}")
	}
	if b.conn == nil {
		conn, err := c.dial(ctx)
		if err != nil {
			b.err = err
			return err
		}
		b.conn = conn
		b.stop = context.AfterFunc(ctx, func() { conn.Close() })
		b.reader = bufio.NewReader(conn)
		b.auth = maps.Clone(auth)
		duration := 60 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			duration = min(duration, time.Until(deadline))
		}
		if duration <= 0 {
			return fail("canceled")
		}
		conn.SetDeadline(time.Now().Add(duration))
		open := Request{V: 1, Op: "root.batch", Auth: auth, Params: json.RawMessage(`{"v":1,"timeout_ms":` + strconv.FormatInt(max(duration.Milliseconds(), 1), 10) + `}`)}
		if err := json.NewEncoder(conn).Encode(open); err != nil {
			b.err = fail(CodeExecutorUnavailable)
			return b.err
		}
		if err := b.read(ctx, nil); err != nil {
			b.err = err
			return err
		}
	} else if !maps.Equal(b.auth, auth) {
		return fail(CodeInvalidRequest)
	}
	wire, err := json.Marshal(Request{V: 1, Op: op, Params: raw})
	if err != nil || len(wire)+1 > requestLimit(op) {
		return fail(CodeExecutorTooLarge)
	}
	b.count++
	if _, err := b.conn.Write(append(wire, '\n')); err != nil {
		b.err = fail(CodeExecutorUnavailable)
		return b.err
	}
	return b.read(ctx, result)
}
func (b *batchClient) read(ctx context.Context, result any) error {
	var line []byte
	var err error
	for {
		var part []byte
		part, err = b.reader.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > MaxFrame {
			b.err = fail(CodeExecutorTooLarge)
			return b.err
		}
		if err != bufio.ErrBufferFull {
			break
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			b.err = fail("canceled")
		} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			b.err = fail(CodeExecutorTimeout)
		} else {
			b.err = fail(CodeExecutorUnavailable)
		}
		return b.err
	}
	if len(line) > MaxFrame {
		b.err = fail(CodeExecutorTooLarge)
		return b.err
	}
	return decodeReply(line, result)
}
