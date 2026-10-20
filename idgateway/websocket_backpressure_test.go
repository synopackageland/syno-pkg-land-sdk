package idgateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type upgradeChecks struct {
	count atomic.Int32
	ready chan struct{}
}

func (c *upgradeChecks) Name(context.Context, int) (string, error) {
	if c.count.Add(1) == 2 {
		close(c.ready)
	}
	return "alice", nil
}

type closingListener struct {
	net.Listener
	closed chan struct{}
}
type closingConnection struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l closingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcp, ok := c.(*net.TCPConn); ok {
		tcp.SetWriteBuffer(1024)
	}
	return &closingConnection{Conn: c, closed: l.closed}, nil
}
func (c *closingConnection) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestWebSocketRevocationClosesFrontendUnderBackpressure(t *testing.T) {
	flooding := make(chan struct{})
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		c, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		rw.Flush()
		io.CopyN(c, zeroStream{}, 256<<10)
		close(flooding)
		io.CopyN(c, zeroStream{}, 64<<20)
	})
	checks := &upgradeChecks{ready: make(chan struct{})}
	f, _ := newFront(t, ModeNone, up, func(cfg *FrontConfig) { cfg.Accounts = checks })
	f.sessions["live"] = session{subject: alice, ip: "127.0.0.1", expires: f.cfg.Now().Add(time.Minute)}
	closed := make(chan struct{})
	srv := httptest.NewUnstartedServer(f)
	srv.Listener = closingListener{Listener: srv.Listener, closed: closed}
	srv.Start()
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.(*net.TCPConn).SetReadBuffer(1024)
	c.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(c, "GET /socket HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nCookie: %s=live\r\n\r\n", addr, srv.URL, f.cookie)
	response, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil || response.StatusCode != 101 {
		t.Fatalf("upgrade %v: %v", response, err)
	}
	for _, ready := range []chan struct{}{checks.ready, flooding} {
		select {
		case <-ready:
		case <-time.After(3 * time.Second):
			t.Fatal("upgrade did not start")
		}
	}
	// The browser deliberately stops reading. Closing only the backend cannot
	// interrupt ReverseProxy's write blocked on this frontend socket.
	f.RevokeUID(alice.UID)
	select {
	case <-closed:
	case <-time.After(7 * time.Second):
		t.Fatal("revoked frontend remained open while its write was blocked")
	}
}
