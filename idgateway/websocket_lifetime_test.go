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
	"sync/atomic"
	"testing"
	"time"
)

type liveDirectory struct{ allowed atomic.Bool }

func (d *liveDirectory) Name(context.Context, int) (string, error) {
	if !d.allowed.Load() {
		return "", ErrNoAccount
	}
	return "alice", nil
}

func (d *liveDirectory) IsAdministrator(context.Context, string) (bool, error) {
	return d.allowed.Load(), nil
}

func (d *liveDirectory) Groups(context.Context, string) ([]string, error) {
	if !d.allowed.Load() {
		return nil, nil
	}
	return []string{"administrators"}, nil
}

type stalledNames struct{}

func (stalledNames) Name(ctx context.Context, _ int) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func TestWebSocketVerifierDeadlineClosesTheTransport(t *testing.T) {
	f, _ := newFront(t, ModeNone, newUpstream(t, nil), func(c *FrontConfig) { c.Accounts = stalledNames{} })
	f.sessions["live"] = session{subject: alice, ip: "127.0.0.1", expires: f.cfg.Now().Add(time.Minute)}
	upstream, peer := net.Pipe()
	defer peer.Close()
	conn := &checkedUpgrade{ReadWriteCloser: upstream, done: make(chan struct{})}
	defer conn.Close()
	finished := make(chan struct{})
	go func() { defer close(finished); f.watchUpgrade(context.Background(), conn, "live", "127.0.0.1") }()
	peer.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("stalled account lookup left transport open: %v", err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("watcher leaked after directory timeout")
	}
}

// This uses a real upgraded TCP connection. A 401 on a later HTTP request
// alone cannot prove that an existing WebSocket stopped forwarding frames.
func TestWebSocketLifetime(t *testing.T) {
	for _, change := range []string{"75-second-expiry", "60-second-real-TTL", "revoke", "logout", "account-deleted", "group-removed", "injected-role-removed"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			upClosed := make(chan struct{})
			up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				defer close(upClosed)
				fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
				rw.Flush()
				io.Copy(conn, rw)
			})
			var now atomic.Int64
			now.Store(time.Now().Unix())
			directory := &liveDirectory{}
			directory.allowed.Store(true)
			mode := ModeNone
			if change == "injected-role-removed" {
				mode = ModeHeader
			}
			f, _ := newFront(t, mode, up, func(c *FrontConfig) {
				c.Now = func() time.Time { return time.Unix(now.Load(), 0) }
				if change == "60-second-real-TTL" {
					c.Now = time.Now
				}
				c.SessionTTL = time.Minute
				if change == "account-deleted" {
					c.Accounts = directory
				}
				if change == "group-removed" {
					c.Access = "dsm-admins"
					c.Membership = directory
				}
				if change == "injected-role-removed" {
					c.Access = "dsm-users"
					c.Membership = directory
				}
			})
			f.sessions["live"] = session{subject: alice, ip: "127.0.0.1", expires: f.cfg.Now().Add(time.Minute)}
			srv := httptest.NewServer(f)
			defer srv.Close()
			addr := strings.TrimPrefix(srv.URL, "http://")
			conn, err := net.DialTimeout("tcp", addr, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(8 * time.Second))
			fmt.Fprintf(conn, "GET /socket HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nCookie: %s=live\r\n\r\n", addr, srv.URL, f.cookie)
			br := bufio.NewReader(conn)
			res, err := http.ReadResponse(br, nil)
			if err != nil || res.StatusCode != 101 {
				t.Fatalf("upgrade: %v %v", res, err)
			}
			if _, err = conn.Write([]byte("before")); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 6)
			if _, err = io.ReadFull(br, buf); err != nil || string(buf) != "before" {
				t.Fatalf("echo %q: %v", buf, err)
			}
			switch change {
			case "75-second-expiry":
				now.Add(75)
			case "revoke":
				f.RevokeUID(alice.UID)
			case "account-deleted", "group-removed", "injected-role-removed":
				directory.allowed.Store(false)
			case "logout":
				req, _ := http.NewRequest("POST", srv.URL+"/_sso/logout", nil)
				req.Header.Set("Origin", srv.URL)
				req.AddCookie(&http.Cookie{Name: f.cookie, Value: "live"})
				client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != 302 {
					t.Fatal(resp.StatusCode)
				}
			}
			deadline := 7 * time.Second
			if change == "60-second-real-TTL" {
				// Prove a live session keeps forwarding across multiple checks;
				// closing every upgrade after one tick must not pass this test.
				observationDeadline := time.Now().Add(75 * time.Second)
				conn.SetDeadline(observationDeadline)
				<-time.After(30 * time.Second)
				if _, err = conn.Write([]byte("alive")); err != nil {
					t.Fatal(err)
				}
				alive := make([]byte, 5)
				if _, err = io.ReadFull(br, alive); err != nil || string(alive) != "alive" {
					t.Fatalf("live WebSocket stopped forwarding before TTL: %q %v", alive, err)
				}
				deadline = time.Until(observationDeadline)
			}
			conn.SetReadDeadline(time.Now().Add(deadline))
			_, err = br.ReadByte()
			if err == nil {
				t.Fatal("revoked WebSocket still delivers data")
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("revoked WebSocket stayed open")
			}
			select {
			case <-upClosed:
			case <-time.After(time.Second):
				t.Fatal("upstream connection leaked")
			}
		})
	}
}
