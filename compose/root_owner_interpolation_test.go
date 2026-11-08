package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
)

func TestRootRuntimeOwnerIsInterpolatedByTheWorker(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Data = nil
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  openlist:\n    image: alpine\n    entrypoint: ['/app/server']\n    environment: {APP_LITERAL: '$${SYNOPKGLAND_UID}'}\n"), 0600)
	r.Services["openlist"] = ServicePolicy{RunAs: &RunAs{Mode: "container-root", Reason: "fixture"}, StorageInit: &StorageInitialization{Runtime: &RuntimeConfiguration{Environment: map[string]RuntimeEnvironment{"APP_TOKEN": {Secret: "app"}}}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	// Container Manager parses the generated YAML again with the package .env.
	worker, err := loader.LoadWithContext(context.Background(), types.ConfigDetails{WorkingDir: r.Root, ConfigFiles: []types.ConfigFile{{Filename: filepath.Join(r.Root, "worker.yaml"), Content: []byte(p.YAML)}}, Environment: types.Mapping{"SYNOPKGLAND_UID": "12345", "SYNOPKGLAND_GID": "12345", "SYNOPKG_PKGVAR": "/state"}}, func(o *loader.Options) { o.SetProjectName("fixture", true); o.ResolvePaths = false })
	if err != nil {
		t.Fatal(err)
	}
	service := worker.Services["openlist"]
	if got := service.Environment[runtimeOwnerEnv]; got == nil || *got != "12345" {
		t.Fatalf("root initializer gets a literal UID: %v", got)
	}
	if got := service.Environment["APP_LITERAL"]; got == nil || *got != "${SYNOPKGLAND_UID}" {
		t.Fatal("App literal was interpolated")
	}
	if service.User != "0:0" {
		t.Fatal("root identity changed")
	}
}
