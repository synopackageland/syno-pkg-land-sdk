package idgateway

import (
	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeingress"
	"testing"
)

func TestComposeFrontAcceptsOnlyItsDerivedEndpoint(t *testing.T) {
	up := newUpstream(t, nil)
	front, _ := newFront(t, ModeNone, up, nil)
	cfg := front.cfg
	cfg.ComposeIngress = &composeingress.Config{Candidate: 2}
	a, _ := composeingress.Allocate(cfg.Package, 2)
	cfg.Upstream = "http://" + a.Address + ":80"
	if _, err := NewFront(cfg); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"http://192.0.2.200:5000", "http://example.invalid:80", "http://127.0.0.1:80", "http://" + a.Address + ":80/?query=1", "http://user@" + a.Address + ":80"} {
		cfg.Upstream = target
		if _, err := NewFront(cfg); err == nil {
			t.Fatalf("foreign target accepted: %s", target)
		}
	}
	cfg.Upstream = "http://" + a.Address + ":80"
	cfg.ComposeIngress = nil
	if _, err := NewFront(cfg); err == nil {
		t.Fatal("native gateway gained arbitrary private-network access")
	}
}
