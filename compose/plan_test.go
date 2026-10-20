package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func upstreamProject(t *testing.T, fixture string) Request {
	t.Helper()
	root := t.TempDir()
	b, err := os.ReadFile("testdata/" + fixture + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "compose.yaml"), b, 0600); err != nil {
		t.Fatal(err)
	}
	req := Request{Root: root, Package: "Fixture", Files: []string{"compose.yaml"}, Network: Network{Egress: "isolated"}, Services: map[string]ServicePolicy{}, Data: map[string]Storage{}}
	if fixture == "openlist" {
		req.Web = &Web{Service: "openlist", Port: 5244, HostPort: 28044}
		req.Data["data"] = Storage{Service: "openlist", Target: "/opt/openlist/data", Storage: "package", Path: "data"}
	}
	if fixture == "immich" {
		env, _ := os.ReadFile("testdata/immich.env")
		os.WriteFile(filepath.Join(root, ".env"), env, 0600)
		req.EnvFiles = []string{".env"}
		req.Web = &Web{Service: "immich-server", Port: 2283, HostPort: 28283}
		req.Data["upload"] = Storage{Service: "immich-server", Target: "/data", Storage: "package", Path: "photos"}
		req.Data["db"] = Storage{Service: "database", Target: "/var/lib/postgresql/data", Storage: "package", Path: "database"}
	}
	return req
}

func TestUpstreamComposeBytesDependenciesAndVolumesSurvive(t *testing.T) {
	for _, fixture := range []string{"openlist", "immich"} {
		t.Run(fixture, func(t *testing.T) {
			req := upstreamProject(t, fixture)
			before, _ := os.ReadFile(filepath.Join(req.Root, "compose.yaml"))
			p, err := Build(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(filepath.Join(req.Root, "compose.yaml"))
			if string(before) != string(after) {
				t.Fatal("upstream changed")
			}
			sum := sha256.Sum256(before)
			if p.Inputs[0].SHA256 != hex.EncodeToString(sum[:]) {
				t.Fatal("upstream provenance lost")
			}
			s := p.Effective.Services[req.Web.Service]
			ingress := p.Effective.Services["sdk-ingress"]
			if len(s.Ports) != 0 || len(ingress.Ports) != 0 || p.Ingress == nil {
				t.Fatalf("public ports survived: %+v", s.Ports)
			}
			if s.User != "${SYNOPKGLAND_UID}:${SYNOPKGLAND_GID}" || len(s.CapDrop) != 1 || s.CapDrop[0] != "ALL" {
				t.Fatalf("identity policy: %+v", s)
			}
			if fixture == "immich" {
				if len(s.DependsOn) != 2 || p.Effective.Services["database"].HealthCheck == nil || len(p.Effective.Volumes) != 1 {
					t.Fatal("dependency, healthcheck or named volume lost")
				}
			}
			if !strings.Contains(p.Override, "!override") {
				t.Fatal("override cannot remove original publish list")
			}
		})
	}
}

func TestMultipleFilesUseReferenceMergeAndThenReplaceEveryPublicPort(t *testing.T) {
	req := upstreamProject(t, "openlist")
	os.WriteFile(filepath.Join(req.Root, "extra.yaml"), []byte("services:\n  openlist:\n    ports: ['12345:5244']\n    environment: {EXTRA: kept}\n"), 0600)
	req.Files = append(req.Files, "extra.yaml")
	p, err := Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	s := p.Effective.Services["openlist"]
	if len(s.Ports) != 0 || p.Ingress == nil || s.Environment["EXTRA"] == nil || *s.Environment["EXTRA"] != "kept" {
		t.Fatal("wrong merge")
	}
}

func TestUnsafeInputsAreRejectedBeforeLoadingOrDeploying(t *testing.T) {
	for _, tc := range []struct{ name, yaml, code string }{
		{"socket", "services:\n  bad:\n    image: alpine\n    volumes: ['/var/run/docker.sock:/run/docker.sock']\n", "COMPOSE_STORAGE_UNMAPPED"},
		{"privileged", "services:\n  bad:\n    image: alpine\n    privileged: true\n", "COMPOSE_PRIVILEGE_UNSUPPORTED"},
		{"host pid", "services:\n  bad:\n    image: alpine\n    pid: host\n", "COMPOSE_PRIVILEGE_UNSUPPORTED"},
		{"build", "services:\n  bad:\n    build: .\n", "COMPOSE_BUILD_UNSUPPORTED"},
		{"external network", "services:\n  bad:\n    image: alpine\n    networks: [foreign]\nnetworks:\n  foreign: {external: true}\n", "COMPOSE_NETWORK_UNSUPPORTED"},
		{"remote include", "include: ['https://evil.invalid/compose.yaml']\nservices: {}\n", "COMPOSE_SOURCE_UNSAFE"},
		{"host hook", "services:\n  bad:\n    image: alpine\n    post_start: [{command: 'touch /host', privileged: true}]\n", "COMPOSE_PRIVILEGE_UNSUPPORTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{Root: t.TempDir(), Package: "Fixture", Files: []string{"compose.yaml"}, Network: Network{Egress: "isolated"}}
			os.WriteFile(filepath.Join(req.Root, "compose.yaml"), []byte(tc.yaml), 0600)
			_, err := Build(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("want %s got %v", tc.code, err)
			}
		})
	}
}

func TestSourceSymlinkAncestorAndEnvEscapeAreRejected(t *testing.T) {
	req := upstreamProject(t, "openlist")
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("stolen"), 0600)
	os.Symlink(outside, filepath.Join(req.Root, "link"))
	for _, file := range []string{"link/secret", "../secret"} {
		req.Files = []string{file}
		if _, err := Build(context.Background(), req); err == nil || !strings.Contains(err.Error(), "COMPOSE_SOURCE_UNSAFE") {
			t.Fatalf("source %s: %v", file, err)
		}
	}
	req.Files = []string{"compose.yaml"}
	req.EnvFiles = []string{"link/secret"}
	if _, err := Build(context.Background(), req); err == nil {
		t.Fatal("env symlink accepted")
	}
}

func TestEgressAndExplicitContainerRootRemainSeparateFromHostElevation(t *testing.T) {
	req := upstreamProject(t, "openlist")
	req.Services["openlist"] = ServicePolicy{Egress: "internet"}
	p, err := Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Effective.Services["openlist"].Networks) < 2 {
		t.Fatal("internet opt-in network missing")
	}
	req.Network.Egress = "allowlist"
	if _, err = Build(context.Background(), req); err == nil || !strings.Contains(err.Error(), "COMPOSE_EGRESS_ALLOWLIST_UNSUPPORTED") {
		t.Fatal(err)
	}
	req.Network.Egress = "isolated"
	req.Services["openlist"] = ServicePolicy{RunAs: &RunAs{Mode: "container-root", Reason: "upstream initializer"}}
	if _, err = Build(context.Background(), req); err == nil || !strings.Contains(err.Error(), "COMPOSE_ROOT_HOST_MOUNT_FORBIDDEN") {
		t.Fatalf("root host bind: %v", err)
	}
}

func TestShareIntentIsBlockedAndCannotAcquireAProject(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.ComposeMounts = true
	r.Shares = map[string]Share{"media": {Select: "admin-after-install", Permission: "ro", Mounts: []Mount{{Service: "openlist", Target: "/media"}}}}
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !p.MountsBlocked {
		t.Fatal("unapproved mount became deployable")
	}
	if !strings.Contains(strings.Join(p.Blockers, ","), "COMPOSE_PROJECT_CONTROL_UNAVAILABLE") {
		t.Fatal("missing fixed-controller blocker")
	}
}

func TestImagePinsCannotReplaceTheUpstreamRepository(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.ImageDigests = map[string]string{"openlistteam/openlist:latest": "attacker/other@sha256:" + strings.Repeat("a", 64)}
	if _, err := Build(context.Background(), r); err == nil {
		t.Fatal("image replaced by unrelated repository")
	}
}

func TestGeneratedOverrideIsAnActualReferenceLoaderOverride(t *testing.T) {
	r := upstreamProject(t, "openlist")
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(r.Root, r.Files[0]))
	merged, err := loader.LoadWithContext(context.Background(), types.ConfigDetails{WorkingDir: r.Root, ConfigFiles: []types.ConfigFile{{Filename: filepath.Join(r.Root, "compose.yaml"), Content: original}, {Filename: filepath.Join(r.Root, "generated.yaml"), Content: []byte(p.Override)}}, Environment: types.Mapping{"SYNOPKG_PKGVAR": "${SYNOPKG_PKGVAR}", "SYNOPKGLAND_UID": "${SYNOPKGLAND_UID}", "SYNOPKGLAND_GID": "${SYNOPKGLAND_GID}"}}, func(o *loader.Options) { o.SetProjectName("fixture", true); o.ResolvePaths = false })
	if err != nil {
		t.Fatal(err)
	}
	raw, err := merged.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	normalized, _ := workerYAML(raw)
	if string(normalized) != p.YAML {
		t.Fatalf("override does not reproduce worker model\n%s\nexpected\n%s", raw, p.YAML)
	}
}
func TestUpstreamHostNetworkNeedsExplicitPolicy(t *testing.T) {
	r := upstreamProject(t, "openlist")
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("services:\n  web: {image: alpine, network_mode: host}\n"), 0600)
	r.Web = nil
	r.Data = nil
	if _, err := Build(context.Background(), r); err == nil {
		t.Fatal("host network silently changed without developer decision")
	}
}

func TestSelectedProfilesAreFlattenedForTheWorker(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Profiles = []string{"selected"}
	os.WriteFile(filepath.Join(r.Root, "extra.yaml"), []byte("services:\n  selected: {image: alpine, profiles: [selected]}\n  skipped: {image: alpine, profiles: [other]}\n"), 0600)
	r.Files = append(r.Files, "extra.yaml")
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Effective.Services["skipped"]; ok {
		t.Fatal("inactive profile included")
	}
	if len(p.Effective.Services["selected"].Profiles) != 0 {
		t.Fatal("worker would disable explicitly selected service")
	}
}

func TestWorkerDoesNotReinterpolateLiteralApplicationValues(t *testing.T) {
	r := upstreamProject(t, "openlist")
	os.WriteFile(filepath.Join(r.Root, "extra.yaml"), []byte("services:\n  openlist:\n    command: ['sh', '-c', 'echo $$HOME']\n    environment: {LITERAL: '$$SDK_GATE_SECRET'}\n"), 0600)
	r.Files = append(r.Files, "extra.yaml")
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.YAML, "$$SDK_GATE_SECRET") || !strings.Contains(p.YAML, "echo $$HOME") {
		t.Fatal("worker would read DSM process environment on its second interpolation")
	}
}
func TestUnsetApplicationEnvironmentNeverInheritsWorkerEnvironment(t *testing.T) {
	r := upstreamProject(t, "openlist")
	os.WriteFile(filepath.Join(r.Root, "extra.yaml"), []byte("services:\n  openlist:\n    environment: [PRIVATE_HOST_SECRET]\n"), 0600)
	r.Files = append(r.Files, "extra.yaml")
	if _, err := Build(context.Background(), r); err == nil {
		t.Fatal("unresolved service environment could inherit DSM worker variables")
	}
}

func TestOverlayFilesWithDifferentPathBasesFailBeforeReferenceLoaderReads(t *testing.T) {
	r := upstreamProject(t, "openlist")
	os.Mkdir(filepath.Join(r.Root, "nested"), 0700)
	os.WriteFile(filepath.Join(r.Root, "nested/base.yaml"), []byte("services:\n  web: {image: alpine}\n"), 0600)
	os.WriteFile(filepath.Join(r.Root, "secret.env"), []byte("HARMLESS=expected\n"), 0600)
	outside := filepath.Join(t.TempDir(), "secret.env")
	os.WriteFile(outside, []byte("ESCAPED_VALUE=must-not-load\n"), 0600)
	os.Symlink(outside, filepath.Join(r.Root, "nested/secret.env"))
	os.WriteFile(filepath.Join(r.Root, "override.yaml"), []byte("services:\n  web:\n    env_file: [secret.env]\n"), 0600)
	r.Files = []string{"nested/base.yaml", "override.yaml"}
	r.Web = nil
	r.Data = nil
	if _, err := Build(context.Background(), r); err == nil {
		t.Fatal("overlay path-base mismatch let the loader read an unchecked symlink")
	}
}
func TestIncludeImplicitDotenvIsInventoriedAndNeverFollowsSymlinks(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.Web = nil
	r.Data = nil
	os.Mkdir(filepath.Join(r.Root, "child"), 0700)
	os.WriteFile(filepath.Join(r.Root, "compose.yaml"), []byte("include: [child/app.yaml]\nservices: {}\n"), 0600)
	os.WriteFile(filepath.Join(r.Root, "child/app.yaml"), []byte("services:\n  web:\n    image: alpine\n    environment: {EXAMPLE: '${VALUE}'}\n"), 0600)
	os.WriteFile(filepath.Join(r.Root, "child/.env"), []byte("VALUE=fixture\n"), 0600)
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, in := range p.Inputs {
		if in.Path == "child/.env" {
			found = true
		}
	}
	if !found {
		t.Fatal("implicit dotenv omitted from source lock")
	}
	os.Remove(filepath.Join(r.Root, "child/.env"))
	secret := filepath.Join(t.TempDir(), "env")
	os.WriteFile(secret, []byte("VALUE=outside\n"), 0600)
	os.Symlink(secret, filepath.Join(r.Root, "child/.env"))
	if _, err = Build(context.Background(), r); err == nil {
		t.Fatal("include implicit dotenv symlink was read")
	}
}

func TestHostIngressCannotRemapTargetPort(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.IdentityMode = "oidc"
	r.Services["openlist"] = ServicePolicy{Network: "host", AddressEnv: "LISTEN_ADDR", Egress: "internet"}
	if _, err := Build(context.Background(), r); err == nil || !strings.Contains(err.Error(), "COMPOSE_OIDC_TOPOLOGY_UNSUPPORTED") {
		t.Fatalf("host ingress silently remapped a port: %v", err)
	}
	r.Web.HostPort = r.Web.Port
	if _, err := Build(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func TestDeclaredHealthCannotChangeProbeOrigin(t *testing.T) {
	for _, path := range []string{"//foreign/", "http://foreign/", "/ok#fragment", "/ok\r\nX: injected"} {
		r := upstreamProject(t, "openlist")
		r.Web.Health = &Health{Path: path, Status: 200}
		if _, err := Build(context.Background(), r); err == nil || !strings.Contains(err.Error(), "COMPOSE_WEB_INVALID") {
			t.Fatalf("%q: %v", path, err)
		}
	}
	r := upstreamProject(t, "openlist")
	r.Web.Health = &Health{Path: "/health?ready=1", Status: 204}
	if _, err := Build(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func TestDirectIngressKeepsIsolatedAppNetworksInternal(t *testing.T) {
	r := upstreamProject(t, "openlist")
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	app := p.Effective.Services["openlist"]
	if len(app.Ports) != 0 {
		t.Fatal("internal-only app still declares an ineffective publish")
	}
	for n := range app.Networks {
		if !p.Effective.Networks[n].Internal {
			t.Fatal("isolated App received outbound network")
		}
	}
	if p.Ingress == nil || !strings.Contains(p.YAML, "sdk_ingress") {
		t.Fatal("missing direct ingress inventory")
	}
	if _, ok := p.Effective.Services["sdk-ingress"]; ok {
		t.Fatal("publish proxy retained")
	}

	r.Services["openlist"] = ServicePolicy{Egress: "internet"}
	p, err = Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Effective.Services["sdk-ingress"]; ok {
		t.Fatal("unnecessary ingress helper")
	}
}

func TestResourcePrefixKeepsPackageIdentityInEveryResource(t *testing.T) {
	r := upstreamProject(t, "openlist")
	r.ResourcePrefix = "fixture-"
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if p.ProjectName != "fixture-fixture" || p.Effective.Name != p.ProjectName {
		t.Fatal(p.ProjectName)
	}
	for _, n := range p.Effective.Networks {
		if !strings.HasPrefix(n.Name, p.ProjectName+"_") {
			t.Fatal(n.Name)
		}
	}
	r.ResourcePrefix = "../foreign/"
	if _, err := Build(context.Background(), r); err == nil {
		t.Fatal("unsafe resource prefix")
	}
}
