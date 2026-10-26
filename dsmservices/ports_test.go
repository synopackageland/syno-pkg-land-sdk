package dsmservices

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDestinationsRetainUDPAndRanges(t *testing.T) {
	defs := Parse("service.sc", []byte("[mixed]\ndst.ports=53,137:139,445-446/all\n[udp]\ndst.ports=123/udp\n"))
	want := []PortRange{{53, 53, true, true}, {137, 139, true, true}, {445, 446, true, true}}
	if !reflect.DeepEqual(defs[0].Destinations, want) || !reflect.DeepEqual(defs[0].Ports, []int{53}) || !reflect.DeepEqual(defs[1].Destinations, []PortRange{{123, 123, false, true}}) {
		t.Fatal(defs)
	}
	// Discovery keeps valid tokens; strict following refuses the whole input.
	raw := []byte("[service]\ndst.ports=80,bad,0,65536,8080/tcp\n")
	if got := Parse("f.sc", raw); len(got[0].Destinations) != 2 {
		t.Fatal(got)
	}
	if _, err := ParseStrict("f.sc", raw); err == nil {
		t.Fatal("invalid list accepted")
	}
}

func TestStrictPreservesOrderTitlesAndProtocols(t *testing.T) {
	raw := []byte("[service]\ntitle=Mail service\ndst.ports=25/tcp\ntitle=\"New title\"\ndst.ports=53/all\n[other]\ndst.ports=137:139/udp,tcp\n")
	got, err := ParseStrict("f.sc", raw)
	if err != nil || len(got) != 3 || got[0].Title != "Mail service" || got[1].Title != "New title" || got[2].Title != "" || got[2].Destinations[0] != (PortRange{137, 139, true, true}) {
		t.Fatal(got, err)
	}
	for _, raw := range []string{"dst.ports=80/tcp", "[a]\ndst.ports=\"80/tcp", "[a]\ntitle=\"", "[a]\ndst.ports=80/tcp/udp", "[a]\n" + strings.Repeat("x", 65536)} {
		if _, err := ParseStrict("f.sc", []byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw[:min(len(raw), 80)])
		}
	}
}

func TestPortSpecsRejectAmbiguousValues(t *testing.T) {
	for _, value := range []string{"", "80", "0/tcp", "65536/tcp", "+80/tcp", "1--2/tcp", "2:1/tcp", "1:/tcp", "1:2-3/tcp", "80/sctp", "80/tcp/udp", "80,/tcp"} {
		if _, err := ParsePortSpec(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	got, err := ParsePortSpec("1-65535/tcp,udp")
	if err != nil || !reflect.DeepEqual(got, []PortRange{{1, 65535, true, true}}) {
		t.Fatal(got, err)
	}
}

func TestReadIsOnDemandAndBoundsFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.sc")
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("[service]\ndst.ports=80/tcp\n")
	if got := Read(dir); len(got) != 1 || got[0].Ports[0] != 80 {
		t.Fatal(got)
	}
	write("[service]\ndst.ports=8080/tcp\n")
	if got := Read(dir); len(got) != 1 || got[0].Ports[0] != 8080 {
		t.Fatal(got)
	}
	write(strings.Repeat("x", fileLimit+1))
	if got := Read(dir); len(got) != 0 {
		t.Fatal(got)
	}
}
