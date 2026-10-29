package ipc

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
)

// PortReservation holds an IPv4 loopback TCP port for a supervised child.
// It exposes no listener, Accept method or HTTP serving surface.
type PortReservation struct {
	listener net.Listener
	port     uint16
	once     sync.Once
	err      error
}

// ReserveLoopback reserves port on 127.0.0.1; zero selects an ephemeral port.
// Release immediately before starting the child. Unless the child supports
// inherited descriptors, another process can win the bind after release:
// detect the child's bind failure and retry rather than assuming ownership.
func ReserveLoopback(ctx context.Context, port uint16) (*PortReservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if err != nil {
		return nil, err
	}
	return &PortReservation{listener: l, port: uint16(l.Addr().(*net.TCPAddr).Port)}, nil
}

func (r *PortReservation) Port() uint16 { return r.port }

func (r *PortReservation) Address() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(int(r.port)))
}

// Release relinquishes the reservation. Repeated calls return the first result.
func (r *PortReservation) Release() error {
	r.once.Do(func() { r.err = r.listener.Close() })
	return r.err
}

// ProbeLoopback checks whether a specific IPv4 loopback TCP port can be bound
// now. It does not reserve the port for the caller or supervise the child.
func ProbeLoopback(ctx context.Context, port uint16) error {
	if port == 0 {
		return errors.New("ipc: probe requires a nonzero port")
	}
	r, err := ReserveLoopback(ctx, port)
	if err != nil {
		return err
	}
	return r.Release()
}
