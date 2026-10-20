package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeNetworkPolicy(t *testing.T) {
	uid := os.Geteuid()
	for _, tc := range []struct {
		name, addr, state, port string
		ok                      bool
	}{
		{"loopback", "0100007F", "0A", "6D64", true},
		{"wildcard", "00000000", "0A", "6D64", false},
		{"other loopback", "0200007F", "0A", "6D64", false},
		{"public gateway", "00000000", "0A", "6D60", true},
		{"unknown port", "00000000", "0A", "6D66", false},
		{"connected", "00000000", "01", "6D66", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := socketFixture(tc.addr, tc.port, tc.state, uid)
			err := checkSocketTable(line, uid, &networkPolicy{Ports: []int{28004}, PublicPorts: []int{28000}}, false)
			if (err == nil) != tc.ok {
				t.Fatalf("got %v, expected ok %v", err, tc.ok)
			}
		})
	}
}

func TestComposeNetworkRequiresOwnedUpstream(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"tcp", "tcp6", "udp", "udp6"} {
		os.WriteFile(filepath.Join(dir, name), []byte("header\n"), 0600)
	}
	p := &networkPolicy{Ports: []int{28004}, PublicPorts: []int{28000}}
	if err := checkNetwork(dir, os.Geteuid(), p); err == nil {
		t.Fatal("missing upstream accepted")
	}
	os.WriteFile(filepath.Join(dir, "tcp"), []byte(socketFixture("0100007F", "6D64", "0A", os.Geteuid()+1)), 0600)
	if err := checkNetwork(dir, os.Geteuid(), p); err == nil {
		t.Fatal("foreign upstream accepted")
	}
	os.WriteFile(filepath.Join(dir, "tcp"), []byte(socketFixture("0100007F", "6D64", "0A", os.Geteuid())), 0600)
	if err := checkNetwork(dir, os.Geteuid(), p); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "udp"), []byte(socketFixture("00000000", "6D66", "07", os.Geteuid())), 0600)
	if err := checkNetwork(dir, os.Geteuid(), p); err == nil {
		t.Fatal("UDP wildcard accepted")
	}
}

func socketFixture(addr, port, state string, uid int) string {
	return fmt.Sprintf("header\n 0: %s:%s 00000000:0000 %s 0:0 0:0 0 %d 0 1\n", addr, port, state, uid)
}

func TestComposeUDPConnectedIsNotAListener(t *testing.T) {
	raw := socketFixture("EA53110A", "BEEF", "01", os.Geteuid())
	if err := checkSocketTable(raw, os.Geteuid(), &networkPolicy{Ports: []int{28004}}, true); err != nil {
		t.Fatal(err)
	}
}

func TestComposeIPv6AndUDPPublicPort(t *testing.T) {
	p := &networkPolicy{Ports: []int{28004}, PublicPorts: []int{28000}}
	for _, tc := range []struct {
		addr, port string
		udp, ok    bool
	}{
		{"00000000000000000000000001000000", "6D64", false, true},
		{"00000000000000000000000000000000", "6D64", false, false},
		{"00000000", "6D60", true, false},
	} {
		state := "0A"
		if tc.udp {
			state = "07"
		}
		err := checkSocketTable(socketFixture(tc.addr, tc.port, state, os.Geteuid()), os.Geteuid(), p, tc.udp)
		if (err == nil) != tc.ok {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

func TestBridgeRequiresLifecycleReadinessContext(t *testing.T) {
	err := checkNetwork(t.TempDir(), os.Geteuid(), &networkPolicy{Mode: "bridge", Ports: []int{28004}})
	if err == nil || !strings.Contains(err.Error(), "COMPOSE_READINESS_INVALID") {
		t.Fatalf("%v", err)
	}
	if err = checkNetwork(t.TempDir(), os.Geteuid(), &networkPolicy{Mode: "settings"}); err != nil {
		t.Fatal(err)
	}
}
