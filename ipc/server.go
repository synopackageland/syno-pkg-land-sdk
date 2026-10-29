package ipc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
)

// Server owns one private socket and every accepted connection, including
// connections hijacked by WebSocket or other HTTP upgrade handlers.
type Server struct {
	http    *http.Server
	mu      sync.Mutex
	conns   map[*trackedConn]struct{}
	closing bool
	done    chan struct{}
	err     error
}

type trackedConn struct {
	net.Conn
	owner *Server
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.owner.mu.Lock()
	delete(c.owner.conns, c)
	c.owner.mu.Unlock()
	return err
}

type trackedListener struct {
	net.Listener
	owner *Server
}

func (l trackedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	t := &trackedConn{Conn: c, owner: l.owner}
	l.owner.mu.Lock()
	if l.owner.closing {
		l.owner.mu.Unlock()
		c.Close()
		return nil, net.ErrClosed
	}
	l.owner.conns[t] = struct{}{}
	l.owner.mu.Unlock()
	return t, nil
}

// StartUnix starts a named socket selected by its full, package-bound path.
// Each server has an independent connection lifetime. No listener is returned.
func StartUnix(ctx context.Context, path string, h http.Handler) (*Server, error) {
	run, name, err := socketPath(path)
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, errors.New("ipc: handler is required")
	}
	return startUnix(ctx, run, name, h)
}

// Wait waits for serving to stop and for the owned socket to be removed.
func (s *Server) Wait() error {
	<-s.done
	return s.err
}

func (s *Server) closeConnections() {
	s.mu.Lock()
	s.closing = true
	conns := make([]*trackedConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

// Close stops admission, closes all connections and waits for socket cleanup.
func (s *Server) Close() error {
	s.closeConnections()
	err := s.http.Close()
	<-s.done
	return err
}

// Shutdown closes upgraded connections as well as active streams, then waits
// for HTTP shutdown and owned socket cleanup. It is suitable for revoke/stop.
// Handlers must also observe request cancellation for their own background work.
func (s *Server) Shutdown(ctx context.Context) error {
	s.closeConnections()
	err := s.http.Shutdown(ctx)
	<-s.done
	return err
}
