package ipc

import (
	"context"
	"net"
	"testing"
)

func TestPackagePeerOptionsRejectUnboundedCapacity(t *testing.T) {
	for _, limit := range []int{-1, 1025} {
		if s, err := StartPackagePeerUnixWithOptions(context.Background(), "/tmp/peers/control.sock", nil, func(context.Context, net.Conn, PeerCredentials) {}, PackagePeerOptions{MaxConnections: limit}); err == nil {
			s.Close()
			t.Fatal("unbounded capacity accepted")
		}
	}
}
