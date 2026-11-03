package bridge

import (
	"bufio"
	"context"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

func TestSlowHelloUsesOperationBudgetAndStillCancels(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan struct{})
			s := &Session{Methods: []string{"slow.read"}, Dial: DialerFunc(func(context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer close(finished)
					defer server.Close()
					c := &fakeConn{r: bufio.NewReader(server), w: server}
					c.recv()
					time.Sleep(30 * time.Second)
					c.send(map[string]any{"t": "hello", "version": 1, "methods": []string{"slow.read"}})
					if c.recv() != nil {
						c.send(map[string]any{"t": "result", "ok": true, "data": "ready"})
					}
				}()
				return client, nil
			})}
			if canceled {
				go func() { time.Sleep(5 * time.Second); cancel() }()
			}
			data, err := s.Call(ctx, "slow.read", nil, &Call{Context: alice})
			<-finished
			if canceled {
				if ctx.Err() == nil || err == nil {
					t.Fatalf("cancellation lost: %s %v", data, err)
				}
			} else if err != nil || string(data) != `"ready"` {
				t.Fatalf("slow hello: %s %v", data, err)
			}
		})
	}
}
