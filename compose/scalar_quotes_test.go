package compose

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"go.yaml.in/yaml/v4"
)

// ambiguousStrings read as numbers, booleans or null when a YAML 1.1 or 1.2
// parser sees them unquoted.
var ambiguousStrings = []string{"10", "0755", "true", "yes", "null", "1e3", "0x1F", "~", "1_000", "0o17", ".5", "on", "No", "-7", "+1", "1:20", ".inf", "0b101", "18446744073709551616"}

// dsmQuoteRewrite models DSM's intermediate Compose rewrite: it re-emits every
// scalar without quotes, so a quoted numeric-looking string becomes a number.
func dsmQuoteRewrite(t *testing.T, raw string) []byte {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			n.Tag, n.Style = "", 0
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(&doc)
	out, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func loadMaterialized(t *testing.T, raw []byte) *types.Project {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := loader.LoadWithContext(context.Background(), types.ConfigDetails{
		WorkingDir:  dir,
		ConfigFiles: []types.ConfigFile{{Filename: filepath.Join(dir, "docker-compose.yml")}},
		Environment: types.Mapping{"SYNOPKGLAND_UID": "138734", "SYNOPKGLAND_GID": "138734", "SYNOPKG_PKGVAR": "/var/packages/Fixture/var"},
	}, func(o *loader.Options) { o.SetProjectName("fixture", true) })
	if err != nil {
		t.Fatalf("Compose rejects the DSM-rewritten worker file: %v\n%s", err, raw)
	}
	return p
}

func quotedList(values []string) string {
	q := make([]string, len(values))
	for i, v := range values {
		q[i] = `"` + v + `"`
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// String-typed Compose values that look like numbers, booleans or null stay
// the same strings after DSM drops their quotes (exec-form healthcheck with
// "--max-time", "10" was rejected as healthcheck.test.N must be a string).
func TestNumericLookingStringsSurviveDSMQuoteRewrite(t *testing.T) {
	r := upstreamProject(t, "openlist")
	var env, labels strings.Builder
	for i, v := range ambiguousStrings {
		env.WriteString("      E" + string(rune('A'+i)) + `: "` + v + "\"\n")
		labels.WriteString("      l" + string(rune('a'+i)) + `: "` + v + "\"\n")
	}
	extra := "services:\n  openlist:\n" +
		"    command: " + quotedList(append([]string{"serve"}, ambiguousStrings...)) + "\n" +
		"    entrypoint: " + quotedList(append([]string{"/entry"}, ambiguousStrings...)) + "\n" +
		"    healthcheck:\n      test: [\"CMD\", \"curl\", \"-fsS\", \"-o\", \"/dev/null\", \"--max-time\", \"10\", \"http://127.0.0.1:5244/ping\"]\n" +
		"    environment:\n" + env.String() +
		"    labels:\n" + labels.String()
	if err := os.WriteFile(filepath.Join(r.Root, "extra.yaml"), []byte(extra), 0600); err != nil {
		t.Fatal(err)
	}
	r.Files = append(r.Files, "extra.yaml")
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	s := loadMaterialized(t, dsmQuoteRewrite(t, p.YAML)).Services["openlist"]

	if want := append([]string{"serve"}, ambiguousStrings...); !slices.Equal(s.Command, want) {
		t.Fatalf("command %q, want %q", s.Command, want)
	}
	if want := append([]string{"/entry"}, ambiguousStrings...); !slices.Equal(s.Entrypoint, want) {
		t.Fatalf("entrypoint %q, want %q", s.Entrypoint, want)
	}
	if want := []string{"CMD", "curl", "-fsS", "-o", "/dev/null", "--max-time", "10", "http://127.0.0.1:5244/ping"}; s.HealthCheck == nil || !slices.Equal(s.HealthCheck.Test, want) {
		t.Fatalf("healthcheck %+v, want %q", s.HealthCheck, want)
	}
	for i, v := range ambiguousStrings {
		key := "E" + string(rune('A'+i))
		if got := s.Environment[key]; got == nil || *got != v {
			t.Errorf("environment %s = %v, want %q", key, got, v)
		}
		if got := s.Labels["l"+string(rune('a'+i))]; got != v {
			t.Errorf("label l%c = %q, want %q", 'a'+i, got, v)
		}
	}
	// SDK values keep their meaning too.
	if got := s.Environment["UMASK"]; got == nil || *got != "022" {
		t.Errorf("UMASK = %v, want 022", got)
	}
	if got := s.Environment[StartupBudgetEnv]; got == nil || *got != "900" {
		t.Errorf("startup budget = %v", got)
	}
	if s.User != "138734:138734" {
		t.Errorf("user %q", s.User)
	}
}

// The plain worker file still loads unchanged where DSM does not rewrite it.
func TestDeferredLiteralsLoadWithoutDSMRewrite(t *testing.T) {
	r := upstreamProject(t, "openlist")
	if err := os.WriteFile(filepath.Join(r.Root, "extra.yaml"), []byte("services:\n  openlist:\n    command: "+quotedList(ambiguousStrings)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r.Files = append(r.Files, "extra.yaml")
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if s := loadMaterialized(t, []byte(p.YAML)).Services["openlist"]; !slices.Equal(s.Command, ambiguousStrings) {
		t.Fatalf("command %q", s.Command)
	}
}

// Apps without numeric-looking strings in string-only positions keep the same
// worker bytes; canonical integers and true/false as environment or label
// values are restored by Compose and stay as they were.
func TestPlainAppsKeepTheirWorkerFile(t *testing.T) {
	for _, fixture := range []string{"vaultwarden", "immich"} {
		r := upstreamProject(t, fixture)
		if fixture == "vaultwarden" {
			r.Web = &Web{Service: "vaultwarden", Port: 80, HostPort: 11001}
			r.Data["data"] = Storage{Service: "vaultwarden", Target: "/data", Storage: "package", Path: "data"}
		}
		p, err := Build(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(p.YAML, literalSentinel) {
			t.Fatalf("%s: unexpected deferred literal\n%s", fixture, p.YAML)
		}
		if !strings.Contains(p.YAML, StartupBudgetEnv+`: "900"`) {
			t.Fatalf("%s: startup budget changed\n%s", fixture, p.YAML)
		}
	}
}

func TestLiteralClassification(t *testing.T) {
	for _, v := range ambiguousStrings {
		if !plainNonString(v) {
			t.Errorf("%q should be ambiguous", v)
		}
	}
	for _, v := range []string{"", "x", "1.2.3", "v10", "10s", "5m0s", "127.0.0.1", "0.0.0.0:80", "-fsS", "--max-time", "http://a:1/", "yes-please", "0x", "1e", "1:2:x", "${X}", "e10", "_1"} {
		if plainNonString(v) {
			t.Errorf("%q should stay plain", v)
		}
	}
}

func TestSchemaDecidesWhereCanonicalValuesStay(t *testing.T) {
	for _, c := range []struct {
		value string
		path  []string
		want  bool
	}{
		{"10", []string{"services", "app", "healthcheck", "test", "6"}, true},
		{"10", []string{"services", "app", "command", "1"}, true},
		{"0", []string{"services", "app", "user"}, true},
		{"true", []string{"services", "app", "entrypoint", "0"}, true},
		{"10", []string{"services", "app", "environment", "A"}, false},
		{"true", []string{"services", "app", "labels", "a"}, false},
		{"0755", []string{"services", "app", "environment", "A"}, true},
		{"yes", []string{"services", "app", "environment", "A"}, true},
		{"134217728", []string{"services", "app", "shm_size"}, false},
		{"10", []string{"services", "app", "unknown_field"}, true},
	} {
		if got := deferLiteral(c.value, c.path); got != c.want {
			t.Errorf("%q at %v: defer=%v, want %v", c.value, c.path, got, c.want)
		}
	}
}
