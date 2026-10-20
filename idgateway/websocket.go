package idgateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	// Active protected upgrades revalidate the gateway session, account name
	// and current admission groups. A lookup failure closes both proxy ends.
	webSocketRecheck       = 5 * time.Second
	webSocketLookupTimeout = 2 * time.Second
)

type checkedUpgrade struct {
	io.ReadWriteCloser
	done     chan struct{}
	mu       sync.Mutex
	frontend net.Conn
	closed   bool
}

type upgradeSession struct {
	id, ip     string
	connection *checkedUpgrade
}

type upgradeWriter struct {
	http.ResponseWriter
	connection *checkedUpgrade
}

func (w *upgradeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *upgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	w.connection.mu.Lock()
	closed := w.connection.closed
	if !closed {
		w.connection.frontend = c
	}
	w.connection.mu.Unlock()
	if closed {
		c.Close()
	}
	return c, rw, nil
}

func (c *checkedUpgrade) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.done)
	frontend, backend := c.frontend, c.ReadWriteCloser
	c.mu.Unlock()
	if frontend != nil {
		frontend.Close()
	}
	if backend != nil {
		return backend.Close()
	}
	return nil
}

func (f *Front) modifyProxyResponse(resp *http.Response) error {
	if err := f.modifyResponse(resp); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return nil
	}
	if _, protected := resp.Request.Context().Value(subjectKey).(Subject); !protected {
		return nil
	}
	s, ok := resp.Request.Context().Value(upgradeKey).(upgradeSession)
	if !ok {
		return fmt.Errorf("upgraded request has no gateway session")
	}
	body, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		return fmt.Errorf("upstream upgrade is not bidirectional")
	}
	conn := s.connection
	conn.ReadWriteCloser = body
	resp.Body = conn
	// Close both ends directly: a blocked frontend write otherwise prevents
	// ReverseProxy from reaching its deferred frontend cleanup after revocation.
	go f.watchUpgrade(resp.Request.Context(), conn, s.id, s.ip)
	return nil
}

func (f *Front) watchUpgrade(ctx context.Context, conn *checkedUpgrade, id, ip string) {
	ticker := time.NewTicker(webSocketRecheck)
	defer ticker.Stop()
	for {
		check, cancel := context.WithTimeout(ctx, webSocketLookupTimeout)
		// Close on a stalled lookup even if a custom directory ignores ctx.
		deadline := time.AfterFunc(webSocketLookupTimeout, func() { conn.Close() })
		allowed := f.upgradeAllowed(check, id, ip)
		stopped := deadline.Stop()
		cancel()
		if !allowed || !stopped {
			conn.Close()
			return
		}
		select {
		case <-conn.done:
			return
		case <-ctx.Done():
			conn.Close()
			return
		case <-ticker.C:
		}
	}
}

func (f *Front) upgradeAllowed(ctx context.Context, id, ip string) bool {
	f.mu.Lock()
	s, ok := f.sessions[id]
	f.mu.Unlock()
	if !ok || !f.cfg.Now().Before(s.expires) || s.ip != ip || f.cfg.Accounts == nil {
		return false
	}
	name, err := f.cfg.Accounts.Name(ctx, s.subject.UID)
	if err != nil || name != s.subject.Name || !f.permits(ctx, s.subject) {
		return false
	}
	// An admitted dsm-users session can still carry an obsolete admin role in
	// injected headers or app tokens. That identity cannot be changed in an
	// already upgraded stream, so force a new handshake on either role change.
	if f.cfg.Mode == ModeToken || f.cfg.Mode == ModeHeader && f.cfg.Headers.Groups != "" {
		admin, err := administrator(ctx, s.subject.Name, f.cfg.Membership)
		if err != nil || admin != s.subject.Admin {
			return false
		}
	}
	// A concurrent logout/revocation must not be undone by a slow lookup.
	f.mu.Lock()
	_, ok = f.sessions[id]
	f.mu.Unlock()
	return ok && f.cfg.Now().Before(s.expires) && ctx.Err() == nil
}
