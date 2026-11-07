package ipc

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func bindTCPPolicy(t *testing.T, raw string) {
	t.Helper()
	oldID, oldPolicy := packageID, tcpListenersPolicy
	packageID = "TestPackage"
	tcpListenersPolicy = base64.RawURLEncoding.EncodeToString([]byte(raw))
	t.Cleanup(func() { packageID, tcpListenersPolicy = oldID, oldPolicy })
}

func availableTCPPort(t *testing.T) int {
	t.Helper()
	r, err := ReserveLoopback(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	port := int(r.Port())
	if err := r.Release(); err != nil {
		t.Fatal(err)
	}
	return port // Runtime bind remains authoritative if another process wins.
}

func TestTCPPolicyRefusals(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"schema":2,"listeners":{"mail":{"scope":"loopback","port":2525}}}`,
		`{"schema":1,"listeners":{}}`, `{"schema":1,"extra":true,"listeners":{}}`,
		`{"schema":1,"listeners":{"Bad":{"scope":"loopback","port":2525}}}`,
		`{"schema":1,"listeners":{"mail":{"scope":"localhost","port":2525}}}`,
		`{"schema":1,"listeners":{"mail":{"scope":"loopback","port":25}}}`,
		`{"schema":1,"listeners":{"mail":{"scope":"loopback","port":65536}}}`,
		`{"schema":1,"listeners":{"mail":{"scope":"loopback","port":2525,"port_range":[2526,3000]}}}`,
		`{"schema":1,"listeners":{"mail":{"scope":"loopback","port":2525,"port_range":[1024,65535,1234]}}}`,
		`{"schema":1,"listeners":{"mail":{"scope":"loopback","port":2525,"port_range":[3000,1024]}}}`,
		`{"schema":1,"listeners":{"mail":{"scope":"loopback","port":2525,"port_range":[0,65535]}}}`,
		`{"schema":1,"listeners":{"mail":{"scope":"loopback","port":2525,"port_range":[1024,65536]}}}`,
		`{"schema":1,"listeners":{"mail":{"scope":"loopback","port":2525,"host":"0.0.0.0"}}}`,
		`{"schema":1,"listeners":{"mail":{"scope":"loopback","port":2525}}} {}`,
	} {
		if _, err := readTCPPolicy(base64.RawURLEncoding.EncodeToString([]byte(raw))); err == nil {
			t.Fatalf("accepted invalid policy: %s", raw)
		}
	}
	for _, raw := range []string{"", "!", string(make([]byte, 32769))} {
		if _, err := readTCPPolicy(raw); err == nil {
			t.Fatal("accepted missing or invalid encoded policy")
		}
	}
}

func TestListenTCPIdentityDeclarationAndRange(t *testing.T) {
	port := availableTCPPort(t)
	bindTCPPolicy(t, fmt.Sprintf(`{"schema":1,"listeners":{"mail":{"scope":"loopback","port":%d}}}`, port))
	for _, test := range []struct {
		id   string
		port int
	}{{"missing", 0}, {"mail", -1}, {"mail", 25}, {"mail", 65536}, {"mail", port + 1}} {
		if l, err := ListenTCP(context.Background(), test.id, test.port); err == nil {
			l.Close()
			t.Fatalf("accepted %#v", test)
		}
	}
	packageID = ""
	if _, err := ListenTCP(context.Background(), "mail", 0); err == nil {
		t.Fatal("unbound package accepted")
	}
	packageID = "TestPackage"
	tcpListenersPolicy = ""
	if _, err := ListenTCP(context.Background(), "mail", 0); err == nil {
		t.Fatal("unbound declaration accepted")
	}
}

func TestListenTCPAdmissionCancellationAndReservationExclusion(t *testing.T) {
	port := availableTCPPort(t)
	bindTCPPolicy(t, fmt.Sprintf(`{"schema":1,"listeners":{"mail":{"scope":"loopback","port":%d,"port_range":[1024,65535]}}}`, port))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l, err := ListenTCP(ctx, "mail", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	addr := l.Addr().(*net.TCPAddr)
	if !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) || addr.Port != port {
		t.Fatalf("unexpected binding %s", addr)
	}
	if r, err := ReserveLoopback(context.Background(), uint16(port)); err == nil {
		r.Release()
		t.Fatal("raw listener did not reserve its port")
	}
	if _, err := ListenTCP(ctx, "mail", port); err == nil {
		t.Fatal("duplicate listener accepted")
	}
	client, err := net.DialTimeout("tcp4", l.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	blocked := make(chan error, 1)
	go func() { _, err := l.Accept(); blocked <- err }()
	cancel()
	select {
	case err := <-blocked:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept after cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not unblock Accept")
	}
	// Cancellation stops admission, not caller-owned accepted sessions.
	server.SetDeadline(time.Now().Add(time.Second))
	client.SetDeadline(time.Now().Add(time.Second))
	go client.Write([]byte("smtp"))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(server, buf); err != nil || string(buf) != "smtp" {
		t.Fatalf("accepted connection lifetime: %s %v", buf, err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	// Port selection is bounded by the declaration and shares reservation rules.
	r, err := ReserveLoopback(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	other := int(r.Port())
	if _, err := ListenTCP(context.Background(), "mail", other); err == nil {
		t.Fatal("reservation was stolen")
	}
	r.Release()
	selected, err := ListenTCP(context.Background(), "mail", other)
	if err != nil {
		t.Fatal(err)
	}
	selected.Close()
}

func TestListenTCPExplicitAllAndCancelledContext(t *testing.T) {
	port := availableTCPPort(t)
	bindTCPPolicy(t, fmt.Sprintf(`{"schema":1,"listeners":{"mail":{"scope":"all","port":%d}}}`, port))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if l, err := ListenTCP(ctx, "mail", 0); err == nil {
		l.Close()
		t.Fatal("cancelled listener accepted")
	}
	l, err := ListenTCP(context.Background(), "mail", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !l.Addr().(*net.TCPAddr).IP.IsUnspecified() {
		t.Fatalf("explicit all ignored: %s", l.Addr())
	}
}
