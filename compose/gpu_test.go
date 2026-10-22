package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/template"
	"go.yaml.in/yaml/v4"
)

func TestGPUDeclarationRemainsOptionalAndBounded(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Devices = &Devices{GPU: &GPU{Services: []string{"openlist"}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if p.Devices == nil || p.Devices.GPU == nil {
		t.Fatal("missing inspect inventory")
	}
	if len(p.Effective.Services["openlist"].Devices) != 0 {
		t.Fatal("build machine device leaked")
	}
	lock, _ := p.LockJSON()
	if !strings.Contains(string(lock), `"gid_source": "videodriver"`) {
		t.Fatal(string(lock))
	}
	for _, services := range [][]string{nil, {"missing"}, {"openlist", "openlist"}} {
		r.Devices.GPU.Services = services
		if _, err = Build(context.Background(), r); err == nil {
			t.Fatalf("accepted %v", services)
		}
	}
	r.Devices.GPU.Services = []string{"openlist"}
	r.Services["openlist"] = ServicePolicy{RunAs: &RunAs{Mode: "container-root", Reason: "upstream"}}
	if _, err = Build(context.Background(), r); err == nil {
		t.Fatal("GPU allowed container root")
	}
}

func TestGPUMaterializationAndSoftwareFallback(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Devices = &Devices{GPU: &GPU{Services: []string{"openlist"}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		nodes     []gpuNode
		available bool
	}{
		{"intel", []gpuNode{{Path: "/dev/dri/renderD128", Major: 226, Minor: 128, GID: 937, Mode: 0660, Character: true, Vendor: "0x8086"}}, true},
		{"absent", nil, false},
		{"wrong-group", []gpuNode{{Path: "/dev/dri/renderD128", Major: 226, Minor: 128, GID: 0, Mode: 0660, Character: true, Vendor: "0x8086"}}, false},
		{"symlink", []gpuNode{{Path: "/dev/dri/renderD128", Major: 226, Minor: 128, GID: 937, Mode: 0660, Character: true, Symlink: true, Vendor: "0x8086"}}, false},
		{"block-device", []gpuNode{{Path: "/dev/dri/renderD128", Major: 226, Minor: 128, GID: 937, Mode: 0660, Vendor: "0x8086"}}, false},
		{"card", []gpuNode{{Path: "/dev/dri/card0", Major: 226, Minor: 0, GID: 937, Mode: 0660, Character: true, Vendor: "0x8086"}}, false},
		{"non-intel", []gpuNode{{Path: "/dev/dri/renderD128", Major: 226, Minor: 128, GID: 937, Mode: 0660, Character: true, Vendor: "0x1002"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := materializeGPU([]byte(p.YAML), p.Devices, tc.nodes, 937)
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			if strings.Contains(text, "group_add:") != tc.available || strings.Contains(text, "/dev/dri/renderD128:/dev/dri/renderD128:rw") != tc.available {
				t.Fatal(text)
			}
			if !tc.available && !strings.Contains(text, "software") {
				t.Fatal(text)
			}
		})
	}
}

func TestGPUPrepareTemplateIntegrity(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Devices = &Devices{GPU: &GPU{Services: []string{"openlist"}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "gpu.template.yaml"), []byte("services: {}"), 0600)
	lock, _ := p.LockJSON()
	var data struct {
		SHA string `json:"effective_sha256"`
	}
	json.Unmarshal(lock, &data)
	if err := prepareGPU(dir, p.Devices, data.SHA); err == nil {
		t.Fatal("modified template accepted")
	}
}

func TestGPUPrepareMaterializesAnIntactTemplate(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Devices = &Devices{GPU: &GPU{Services: []string{"openlist"}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "gpu.template.yaml"), []byte(p.YAML), 0600)
	if err := prepareGPU(dir, p.Devices, p.SHA256); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "SYNOPKGLAND_GPU_STATUS") {
		t.Fatal(string(raw))
	}
	// Hardware evidence is separate: this test verifies the real atomic materializer.
	st, _ := os.Stat(filepath.Join(dir, "docker-compose.yml"))
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
}

// DSM rewrites Compose and drops quotes around numeric-looking scalars.
// Defer the trusted constant GID until Compose interpolates the rewritten file.
func TestGPUGroupSurvivesNativeWorkerScalarRewrite(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Devices = &Devices{GPU: &GPU{Services: []string{"openlist"}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := materializeGPU([]byte(p.YAML), p.Devices, []gpuNode{{Path: "/dev/dri/renderD128", Major: 226, Minor: 128, GID: 937, Mode: 0660, Character: true, Vendor: "0x8086"}}, 937)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	group := doc["services"].(map[string]any)["openlist"].(map[string]any)["group_add"].([]any)[0].(string)
	env := func(key string) (string, bool) {
		if key == "SYNOPKGLAND_GID" {
			return "138734", true
		}
		return "", false
	}
	if _, err := strconv.Atoi(group); err == nil {
		t.Fatal("native worker would emit an integer group_add")
	}
	final, err := template.Substitute(group, env)
	if err != nil || final != "937" {
		t.Fatalf("final GID %q: %v", final, err)
	}
}
