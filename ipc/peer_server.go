package ipc

import (
	"context"
	"net"
	"sync"
)

// PeerServer owns a protected peer listener and its verified connections.
// StopAccepting ends admission; Close or context cancellation ends connections.
// Methods are safe to call concurrently, including from a handler.
type PeerServer struct {
	listener  net.Listener
	cleanup   func()
	ctx       context.Context
	cancel    context.CancelFunc
	stopOnce  sync.Once
	closeOnce sync.Once
	done      chan struct{}
	closed    chan struct{}
	err       error // published by closing done
	mu        sync.Mutex
	closing   bool
	conns     map[*peerConn]struct{}
	slots     chan struct{}
}

type peerConn struct {
	net.Conn
	owner       *PeerServer
	once        sync.Once
	closed      bool // guarded by owner.mu
	handlerDone bool // guarded by owner.mu
}

func (c *peerConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.owner.mu.Lock()
		delete(c.owner.conns, c)
		c.closed = true
		if c.handlerDone {
			<-c.owner.slots
		}
		c.owner.mu.Unlock()
	})
	return err
}

// Hold the slot until both the callback and its connection have finished.
// Closing early cannot create unbounded still-running callbacks; returning
// early cannot create unbounded retained connections.
func (c *peerConn) handlerReturned() {
	c.owner.mu.Lock()
	c.handlerDone = true
	if c.closed {
		<-c.owner.slots
	}
	c.owner.mu.Unlock()
}

// StopAccepting closes the listener, removes the owned socket and waits for
// the accept loop to exit. It preserves verified peers and their context.
// It does not wait for callbacks and can be called by one of them.
func (s *PeerServer) StopAccepting() error {
	s.stopOnce.Do(func() { s.listener.Close() })
	return s.Wait()
}

// Wait waits for admission to stop and the owned socket to be removed, not
// for handlers or retained connections. Explicit StopAccepting/Close return
// nil; cancellation and unexpected accept/cleanup failures are reported.
func (s *PeerServer) Wait() error {
	<-s.done
	return s.err
}

func (s *PeerServer) closeConnections() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		conns := make([]*peerConn, 0, len(s.conns))
		for c := range s.conns {
			conns = append(conns, c)
		}
		s.mu.Unlock()
		s.cancel()
		close(s.closed)
		for _, c := range conns {
			c.Close()
		}
	})
}

// Close cancels handler contexts, closes all accepted connections and stops
// admission, waiting for socket cleanup. Handlers must stop their own work.
func (s *PeerServer) Close() error {
	s.closeConnections()
	return s.StopAccepting()
}
