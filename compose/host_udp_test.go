package compose

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func hostUDPRequest(t *testing.T) Request {
	t.Helper()
	r := upstreamProject(t, "openlist")
	r.IdentityMode = "oidc"
	r.Network.Egress = "internet"
	r.Web.HostPort = r.Web.Port
	r.Services["openlist"] = ServicePolicy{Network: "host", AddressEnv: "BIND_ADDRESS"}
	return r
}

func TestHostUDPPlanAndLock(t *testing.T) {
	r := hostUDPRequest(t)
	r.HostUDP = []HostUDPListener{{Port: 5353, Purpose: "mdns"}, {Port: 1900, Purpose: "ssdp"}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	want := []HostUDPListener{{Port: 1900, Purpose: "ssdp"}, {Port: 5353, Purpose: "mdns"}}
	if !reflect.DeepEqual(p.HostUDP, want) {
		t.Fatalf("inventory: %+v", p.HostUDP)
	}
	lock, err := p.LockJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		HostUDP []HostUDPListener `json:"host_udp"`
	}
	if err := json.Unmarshal(lock, &decoded); err != nil || !reflect.DeepEqual(decoded.HostUDP, want) {
		t.Fatalf("lock: %s, %v", lock, err)
	}
	s := p.Effective.Services["openlist"]
	if s.User != PackageUser || len(s.CapAdd) != 0 || len(s.CapDrop) != 1 || s.CapDrop[0] != "ALL" || s.Privileged {
		t.Fatalf("privilege widened: %+v", s)
	}
	if r.HostUDP[0].Port != 5353 {
		t.Fatal("mutated caller declaration")
	}
}

func TestHostUDPRejectsInvalidDeclarations(t *testing.T) {
	for _, ports := range [][]HostUDPListener{
		{{Port: 0, Purpose: "invalid"}}, {{Port: 53, Purpose: "dns"}}, {{Port: 1023, Purpose: "low"}},
		{{Port: 65536, Purpose: "large"}}, {{Port: 5353}}, {{Port: 5353, Purpose: "bad purpose"}},
		{{Port: 5353, Purpose: strings.Repeat("a", 65)}},
		{{Port: 5353, Purpose: "mdns"}, {Port: 5353, Purpose: "duplicate"}},
	} {
		r := hostUDPRequest(t)
		r.HostUDP = ports
		if _, err := Build(context.Background(), r); err == nil || !strings.Contains(err.Error(), "COMPOSE_HOST_UDP_INVALID") {
			t.Fatalf("%+v: %v", ports, err)
		}
	}
	r := hostUDPRequest(t)
	for port := 1024; port < 1057; port++ {
		r.HostUDP = append(r.HostUDP, HostUDPListener{Port: port, Purpose: "discovery"})
	}
	if _, err := Build(context.Background(), r); err == nil {
		t.Fatal("unbounded declaration")
	}
	for _, port := range []int{1024, 65535} {
		r := hostUDPRequest(t)
		r.HostUDP = []HostUDPListener{{Port: port, Purpose: "custom-discovery"}}
		if _, err := Build(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHostUDPRequiresVerifiedHostWeb(t *testing.T) {
	for _, headless := range []bool{false, true} {
		r := upstreamProject(t, "openlist")
		r.HostUDP = []HostUDPListener{{Port: 5353, Purpose: "mdns"}}
		if headless {
			r.Web = nil
		}
		if _, err := Build(context.Background(), r); err == nil || !strings.Contains(err.Error(), "COMPOSE_HOST_UDP_INVALID") {
			t.Fatalf("headless=%v: %v", headless, err)
		}
	}
}
