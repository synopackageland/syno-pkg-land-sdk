package compose

import (
	"context"
	"encoding/json"
	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"testing"
)

func TestNoPublishIngressIsDeterministicAndReplacesEveryUpstreamPort(t *testing.T) {
	r := upstreamProject(t, "openlist")
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Effective.Services["sdk-ingress"]; ok {
		t.Fatal("unnecessary publish proxy remains")
	}
	for name, s := range p.Effective.Services {
		if len(s.Ports) != 0 {
			t.Fatalf("%s publishes ports: %+v", name, s.Ports)
		}
	}
	if p.Ingress == nil || p.Ingress.Address == "" || p.Ingress.Subnet == "" || p.Ingress.Port != 5244 {
		t.Fatalf("missing fixed endpoint: %+v", p.Ingress)
	}
	web := p.Effective.Services[r.Web.Service]
	n := web.Networks["sdk_ingress"]
	if n == nil || n.Ipv4Address != p.Ingress.Address || !p.Effective.Networks["sdk_ingress"].Internal {
		t.Fatal("ingress is not a dedicated internal bridge")
	}
	again, err := Build(context.Background(), r)
	if err != nil || again.Ingress.Address != p.Ingress.Address {
		t.Fatal("unstable allocation")
	}
	r.Package = "OtherApp"
	other, err := Build(context.Background(), r)
	if err != nil || other.Ingress.Subnet == p.Ingress.Subnet {
		t.Fatal("fixture Apps overlap")
	}
	var lock map[string]any
	raw, _ := p.LockJSON()
	json.Unmarshal(raw, &lock)
	if lock["ingress"] == nil {
		t.Fatal("endpoint absent from lock")
	}
	merged, err := loader.LoadWithContext(context.Background(), types.ConfigDetails{WorkingDir: r.Root, ConfigFiles: []types.ConfigFile{{Filename: r.Root + "/compose.yaml", Content: []byte(p.Inputs[0].Content)}, {Filename: r.Root + "/generated.override.yaml", Content: []byte(p.Override)}}, Environment: types.Mapping{}}, func(o *loader.Options) { o.SetProjectName(p.ProjectName, true) })
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range merged.Services {
		if len(s.Ports) != 0 {
			t.Fatalf("replacement merge retained publishes on %s", name)
		}
	}
}
