package ipc

import (
	"context"
	"net"
	"strconv"
	"testing"
)

func TestLoopbackReservationAndProbe(t *testing.T) {
	ctx := context.Background()
	r, err := ReserveLoopback(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	if r.Port() == 0 || r.Address() != net.JoinHostPort("127.0.0.1", strconv.Itoa(int(r.Port()))) {
		t.Fatalf("invalid reservation: %s", r.Address())
	}
	if err := ProbeLoopback(ctx, r.Port()); err == nil {
		t.Fatal("reserved port reported free")
	}
	if _, err := ReserveLoopback(ctx, r.Port()); err == nil {
		t.Fatal("reserved port allocated twice")
	}
	if err := r.Release(); err != nil {
		t.Fatal(err)
	}
	if err := r.Release(); err != nil {
		t.Fatal(err)
	}
	if err := ProbeLoopback(ctx, r.Port()); err != nil {
		t.Fatal(err)
	}
	if err := ProbeLoopback(ctx, 0); err == nil {
		t.Fatal("zero is not a specific probe port")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ReserveLoopback(cancelled, 0); err == nil {
		t.Fatal("canceled reservation accepted")
	}
}
