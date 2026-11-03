package ipc

import (
	"context"
	"net"
	"testing"
)

func TestPackagePeerOptionsRejectUnboundedCapacity(t *testing.T) {
	for _, limit := range []int{-1, MaxPackagePeerConnections + 1} {
		if s, err := StartPackagePeerUnixWithOptions(context.Background(), "/tmp/peers/control.sock", nil, func(context.Context, net.Conn, PeerCredentials) {}, PackagePeerOptions{MaxConnections: limit}); err == nil {
			s.Close()
			t.Fatal("unbounded capacity accepted")
		}
	}
}

func TestPackagePeerCeilingFitsThousandsOfPeers(t *testing.T) {
	// Thousands of retained peers plus handshake/probe headroom must fit.
	if MaxPackagePeerConnections < 3000+3000/4 {
		t.Fatalf("ceiling %d is below the multi-thousand peer target", MaxPackagePeerConnections)
	}
}
