package compose

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
)

// Keep the CLI fixture's runtime contract when replacing its wiki-sourced YAML.
func TestVaultwardenFixtureRuntimeContract(t *testing.T) {
	r := upstreamProject(t, "vaultwarden")
	p, err := loader.LoadWithContext(context.Background(), types.ConfigDetails{
		WorkingDir:  r.Root,
		ConfigFiles: []types.ConfigFile{{Filename: filepath.Join(r.Root, "compose.yaml")}},
	}, func(o *loader.Options) { o.SetProjectName("fixture", true) })
	if err != nil {
		t.Fatal(err)
	}
	s := p.Services["vaultwarden"]
	if len(p.Services) != 1 || s.Image != "vaultwarden/server:latest" || s.ContainerName != "vaultwarden" || s.Restart != "always" {
		t.Fatalf("service contract changed: %+v", s)
	}
	if len(s.Environment) != 1 || s.Environment["SIGNUPS_ALLOWED"] == nil || *s.Environment["SIGNUPS_ALLOWED"] != "true" {
		t.Fatalf("environment changed: %+v", s.Environment)
	}
	if len(s.Ports) != 1 || s.Ports[0].Published != "11001" || s.Ports[0].Target != 80 || s.Ports[0].Protocol != "tcp" {
		t.Fatalf("port mapping changed: %+v", s.Ports)
	}
	if len(s.Volumes) != 1 || s.Volumes[0].Type != "bind" || s.Volumes[0].Source != filepath.Join(r.Root, "vw-data") || s.Volumes[0].Target != "/data" || s.Volumes[0].ReadOnly {
		t.Fatalf("storage changed: %+v", s.Volumes)
	}
	if s.Volumes[0].Bind == nil || s.Volumes[0].Bind.CreateHostPath != types.OptOut(true) {
		t.Fatal("bind no longer creates the data directory")
	}

	r.Web = &Web{Service: "vaultwarden", Port: 80, HostPort: 11001}
	r.Data["data"] = Storage{Service: "vaultwarden", Target: "/data", Storage: "package", Path: "data"}
	plan, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	app := plan.Effective.Services["vaultwarden"]
	if app.Image != s.Image || app.Environment["SIGNUPS_ALLOWED"] == nil || *app.Environment["SIGNUPS_ALLOWED"] != "true" || len(app.Ports) != 0 || plan.Ingress == nil || plan.Ingress.Service != "vaultwarden" || plan.Ingress.Port != 80 {
		t.Fatalf("planner lost application contract: %+v", app)
	}
}
