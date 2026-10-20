package composeingress

import "testing"

func TestCandidatesAndRouteConflicts(t *testing.T) {
	a, err := Allocate("ExampleApp", 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Allocate("ExampleApp", 1)
	c, _ := Allocate("OtherApp", 0)
	if a.Subnet == b.Subnet || a.Subnet == c.Subnet {
		t.Fatal("fixture candidates collide")
	}
	if _, err := Allocate("ExampleApp", 256); err == nil {
		t.Fatal("unbounded candidates")
	}
	raw := "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\neth0 00000000 0100000A 0003 0 0 0 00000000 0 0 0\neth1 0000800A 00000000 0001 0 0 0 000080FF 0 0 0\n"
	if err := CheckRoutes(raw, a.Subnet, ""); err == nil {
		t.Fatal("overlapping LAN route accepted")
	}
	if err := CheckRoutes(raw, a.Subnet, a.Subnet); err == nil {
		t.Fatal("upgrade exemption allowed broader foreign network")
	}
	if err := CheckRoutes("broken", a.Subnet, ""); err == nil {
		t.Fatal("unreadable inventory accepted")
	}
}

func TestInterfaceAddressesCannotHideBehindPolicyRouting(t *testing.T) {
	a, _ := Allocate("ExampleApp", 0)
	if err := CheckAddresses(map[string][]string{"lan0": {a.Address + "/32"}}, a.Subnet, ""); err == nil {
		t.Fatal("local interface overlap accepted")
	}
	if err := CheckAddresses(map[string][]string{"docker-owned": {a.Address + "/29"}}, a.Subnet, a.Subnet); err != nil {
		t.Fatal(err)
	}
	if err := CheckAddresses(map[string][]string{"lan0": {a.Address + "/29"}}, a.Subnet, a.Subnet); err == nil {
		t.Fatal("foreign LAN inherited upgrade exemption")
	}
	if err := CheckAddresses(map[string][]string{"lan0": {"2001:db8::1/64"}}, a.Subnet, ""); err != nil {
		t.Fatal(err)
	}
}

func TestExactBridgeReuseNeedsThePreviousPrivateAllocation(t *testing.T) {
	raw := "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\nbr-owned 380200C0 00000000 0001 0 0 0 F8FFFFFF 0 0 0\n"
	if err := CheckRoutes(raw, "192.0.2.56/29", ""); err == nil {
		t.Fatal("foreign exact bridge accepted")
	}
	if err := CheckRoutes(raw, "192.0.2.56/29", "192.0.2.56/29"); err != nil {
		t.Fatal(err)
	}
}
