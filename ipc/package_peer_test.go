package ipc

import (
	"context"
	"net"
	"runtime"
	"testing"
)

func TestPackagePeerArgumentsAndUnsupportedPlatform(t *testing.T) {
	h := func(context.Context, net.Conn, PeerCredentials) {}
	for _, path := range []string{"control.sock", "/tmp/control", "/tmp/a/../control.sock", "/tmp/.sock", "/tmp/control.sock/"} {
		if s, err := StartPackagePeerUnix(context.Background(), path, nil, h); err == nil {
			s.Close()
			t.Fatalf("invalid path accepted: %q", path)
		}
	}
	if _, err := StartPackagePeerUnix(context.Background(), "/tmp/peers/control.sock", nil, nil); err == nil {
		t.Fatal("nil handler accepted")
	}
	if runtime.GOOS != "linux" {
		if _, err := StartPackagePeerUnix(context.Background(), "/tmp/peers/control.sock", nil, h); err == nil {
			t.Fatal("unsupported platform admitted")
		}
	}
}
