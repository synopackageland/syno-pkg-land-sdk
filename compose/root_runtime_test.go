package compose

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitRootRuntimeEnvironmentRetainsIsolation(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Data = nil
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/server']\n    read_only: true\n    volumes: ['state:/data']\nvolumes:\n  state: {}\n"), 0600)
	r.Services["openlist"] = ServicePolicy{RunAs: &RunAs{Mode: "container-root", Reason: "Read retained private app data", Capabilities: []string{"DAC_OVERRIDE"}}, StorageInit: &StorageInitialization{Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"APP_TOKEN": {Secret: "app"}}}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	s := p.Effective.Services["openlist"]
	if s.User != "0:0" || !s.ReadOnly || len(s.CapAdd) != 1 || s.CapAdd[0] != "DAC_OVERRIDE" || len(s.CapDrop) != 1 || s.CapDrop[0] != "ALL" {
		t.Fatalf("isolation changed: %+v", s)
	}
	if !p.Services[0].ContainerRoot || p.Services[0].RootReason == "" || p.Services[0].HostElevation {
		t.Fatal("missing explicit root inventory")
	}
	for _, m := range s.Volumes {
		if m.Type == "bind" && (!m.ReadOnly || !strings.HasPrefix(m.Source, "/var/packages/Fixture/target/compose/")) {
			t.Fatalf("unexpected host bind: %+v", m)
		}
	}
	for _, kind := range []string{"directory", "prepare", "periodic", "missing-reason", "host-bind", "hidden-helper"} {
		t.Run(kind, func(t *testing.T) {
			policy := r.Services["openlist"]
			init := *policy.StorageInit
			runtime := *init.Runtime
			init.Runtime = &runtime
			policy.StorageInit = &init
			runAs := *policy.RunAs
			policy.RunAs = &runAs
			rr := r
			rr.Services = map[string]ServicePolicy{"openlist": policy}
			switch kind {
			case "directory":
				init.Directories = []WritableDirectory{{Base: "/data", Path: "home"}}
			case "prepare":
				runtime.Prepare = []PrepareCommand{{Argv: []string{"/bin/true"}}}
			case "periodic":
				runtime.Periodic = []PeriodicCommand{{Argv: []string{"/bin/true"}, IntervalSeconds: 60, TimeoutSeconds: 5}}
			case "missing-reason":
				runAs.Reason = ""
			case "host-bind":
				rr.Data = map[string]Storage{"data": {Service: "openlist", Target: "/data", Path: "data", Storage: "private"}}
			case "hidden-helper":
				os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/server']\n    volumes: ['state:/synopkgland']\nvolumes:\n  state: {}\n"), 0600)
			}
			if _, err := Build(context.Background(), rr); err == nil {
				t.Fatal("unsafe root initialization accepted")
			}
		})
	}
}

func TestPackageRuntimeStillRequiresPrivateDirectories(t *testing.T) {
	if err := InitializeRuntime(context.Background(), StorageInitialization{Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{}}}); err == nil {
		t.Fatal("empty package initializer accepted")
	}
}

func TestRuntimeSecretOwnerIsExplicit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seed")
	os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600)
	if _, err := readRuntimeSecretAs(path, uint32(os.Geteuid())+1); err == nil {
		t.Fatal("foreign owner accepted")
	}
	if _, err := readRuntimeSecretAs(path, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
}
