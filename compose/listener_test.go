package compose

import (
	"context"
	"strings"
	"testing"
)

func TestStructuredListenerUsesOnlyDeclaredWebPort(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.IdentityMode = "oidc"
	r.Network.Egress = "internet"
	r.Web.HostPort = 0
	r.Services["openlist"] = ServicePolicy{Network: "host", Listener: &Listener{Env: "LISTEN", Format: "host-port"}, Environment: map[string]string{"LISTEN": "0.0.0.0:9999"}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	s := p.Effective.Services["openlist"]
	if *s.Environment["LISTEN"] != "127.0.0.1:5244" || s.NetworkMode != "host" || len(s.Ports) != 0 {
		t.Fatalf("unsafe listener: %+v", s)
	}
	for _, mutate := range []func(){
		func() {
			r.Services["openlist"] = ServicePolicy{Network: "host", Listener: &Listener{Env: "LISTEN", Format: "wildcard"}}
		},
		func() {
			r.Services["openlist"] = ServicePolicy{Network: "host", AddressEnv: "OTHER", Listener: &Listener{Env: "LISTEN", Format: "host-port"}}
		},
		func() {
			r.Services["openlist"] = ServicePolicy{Listener: &Listener{Env: "LISTEN", Format: "host-port"}}
		},
	} {
		mutate()
		if _, err := Build(context.Background(), r); err == nil || !strings.Contains(err.Error(), "COMPOSE_OIDC_TOPOLOGY_UNSUPPORTED") {
			t.Fatalf("invalid listener accepted: %v", err)
		}
	}
}
