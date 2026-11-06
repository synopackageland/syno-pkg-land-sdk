package nativehttp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/idgateway"
)

func buildConfig() BuildConfig {
	return BuildConfig{
		Package: Package{ID: "ExampleApp", Version: "1.0.0-1", DisplayName: "Example App"},
		Aliases: []string{"example"}, Provenance: Provenance{DevelopedBy: "third-party", Generator: "metadata-fixture"},
		Backend: "compose", Transport: "native-http",
		Gateway: idgateway.Config{Package: "ExampleApp", IngressPath: "/synopkgland/ExampleApp/", Access: "dsm-users", UpstreamAccess: "protected", UpstreamAuthPaths: []string{"/api/"}},
	}
}
func TestSemanticConformance(t *testing.T) {
	var cases struct {
		Valid   []string
		Invalid []struct{ File, Stage string }
	}
	if e := json.Unmarshal(fixture(t, "cases.json"), &cases); e != nil {
		t.Fatal(e)
	}
	for _, path := range cases.Valid {
		t.Run(path, func(t *testing.T) {
			if _, e := ParseRegistry(fixture(t, path), buildConfig()); e != nil {
				t.Fatal(e)
			}
			if _, e := Export(fixture(t, path), buildConfig()); e != nil {
				t.Fatal(e)
			}
		})
	}
	for _, tc := range cases.Invalid {
		t.Run(tc.File, func(t *testing.T) {
			c := buildConfig()
			if tc.Stage == "semantic-admin" {
				c.Gateway.Entry = "admin-only"
			}
			if _, e := ParseRegistry(fixture(t, tc.File), c); e == nil {
				t.Fatal("invalid registry accepted")
			}
		})
	}
}
func TestNativeAndComposeExportTheSameBindings(t *testing.T) {
	c := buildConfig()
	raw := fixture(t, "valid/header-page-files.json")
	a, e := Export(raw, c)
	if e != nil {
		t.Fatal(e)
	}
	c.Backend = "native"
	b, e := Export(raw, c)
	if e != nil {
		t.Fatal(e)
	}
	if !same(a, b) {
		t.Fatal("backend changed metadata semantics")
	}
	if a.Descriptor.Payload.EntryPolicyDigest != c.Gateway.EntryPolicyDigest() {
		t.Fatal("gateway policy digest drift")
	}
	for _, mode := range []string{"", "rpc", "external"} {
		c.Transport = mode
		if _, e := Export(raw, c); e == nil {
			t.Fatalf("legacy transport accepted: %q", mode)
		}
	}
}
func TestArtifactEqualityAndDigestDrift(t *testing.T) {
	a, e := Export(fixture(t, "valid/header-page-files.json"), buildConfig())
	if e != nil {
		t.Fatal(e)
	}
	db, ib, e := a.Encode()
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Descriptor, *Inventory)
	}{
		{"package", func(d *Descriptor, i *Inventory) { d.Payload.Package.Version = "2.0-1" }},
		{"CGI", func(d *Descriptor, i *Inventory) { d.Payload.Transport.Path = "/other/cgi/api.cgi" }},
		{"auth", func(d *Descriptor, i *Inventory) { d.Payload.Execution.Auth.Prefix = "Bearer " }},
		{"base", func(d *Descriptor, i *Inventory) { d.Payload.Execution.BasePath = "/other/" }},
		{"entry", func(d *Descriptor, i *Inventory) { d.Payload.Entry = "admin-only" }},
		{"policy digest", func(d *Descriptor, i *Inventory) { d.Payload.EntryPolicyDigest = strings.Repeat("0", 64) }},
		{"registry digest", func(d *Descriptor, i *Inventory) { d.Payload.RegistrySHA256 = strings.Repeat("0", 64) }},
		{"inventory binding", func(d *Descriptor, i *Inventory) {
			o := i.Bindings["documents.list"]
			o.HTTP.Path = "/api/other/"
			i.Bindings["documents.list"] = o
		}},
		{"command binding", func(d *Descriptor, i *Inventory) {
			o := d.Payload.Commands["documents.list"]
			o.HTTP.Path = "/api/other/"
			d.Payload.Commands["documents.list"] = o
		}},
		{"coverage", func(d *Descriptor, i *Inventory) { i.UpstreamAuthPaths = []string{"/apix/"} }},
		{"feature", func(d *Descriptor, i *Inventory) { d.Payload.Features = []string{Capability} }},
		{"description hash", func(d *Descriptor, i *Inventory) { d.SHA256 = strings.Repeat("0", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var d Descriptor
			var i Inventory
			json.Unmarshal(db, &d)
			json.Unmarshal(ib, &i)
			tc.mutate(&d, &i)
			dr, _ := json.Marshal(d)
			ir, _ := json.Marshal(i)
			if _, e := ValidateArtifacts(dr, ir); e == nil {
				t.Fatal("metadata drift accepted")
			}
		})
	}
}
func TestWireCanonicalSemanticsSurviveExport(t *testing.T) {
	raw := fixture(t, "valid/greader-invoke.json")
	var r map[string]any
	json.Unmarshal(raw, &r)
	op := r["operations"].(map[string]any)["feeds.list"].(map[string]any)
	h := op["http"].(map[string]any)
	h["headers"] = map[string]any{}
	h["query"] = map[string]any{}
	op["resultSchema"] = map[string]any{"type": "number", "minimum": 1e-7, "maximum": 1e21, "description": "文字 <>&"}
	b, _ := json.Marshal(r)
	a, e := Export(b, buildConfig())
	if e != nil {
		t.Fatal(e)
	}
	want, _ := Digest(b)
	if want != a.Inventory.RegistrySHA256 {
		t.Fatal("wire hash changed")
	}
}
func TestAdditionalSemanticBoundaries(t *testing.T) {
	raw := fixture(t, "valid/header-page-files.json")
	for _, tc := range []struct {
		name   string
		mutate func(*Registry, *BuildConfig)
	}{
		{"auth traversal", func(r *Registry, c *BuildConfig) { r.NativeHTTP.Auth.Verify.Path = "/api/../private" }},
		{"auth outside paths", func(r *Registry, c *BuildConfig) { r.NativeHTTP.Auth.Verify.Path = "/private/profile" }},
		{"lookalike prefix", func(r *Registry, c *BuildConfig) {
			o := r.Operations["documents.list"]
			o.HTTP.Path = "/apix/data"
			r.Operations["documents.list"] = o
		}},
		{"mixed placeholder", func(r *Registry, c *BuildConfig) {
			o := r.Operations["documents.download"]
			o.HTTP.Path = "/api/item-{id}/"
			r.Operations["documents.download"] = o
		}},
		{"origin credential page", func(r *Registry, c *BuildConfig) { r.NativeHTTP.Auth.CredentialPage = "//example.org/profile" }},
		{"credential query", func(r *Registry, c *BuildConfig) { r.NativeHTTP.Auth.CredentialPage = "profile?access_token=fixture" }},
		{"callback", func(r *Registry, c *BuildConfig) { r.NativeHTTP.Auth.CredentialPage = "profile?callback=external" }},
		{"duplicate page param", func(r *Registry, c *BuildConfig) {
			o := r.Operations["documents.list"]
			o.Flow.Pagination.SizeParam = "page"
			r.Operations["documents.list"] = o
		}},
		{"override cursor", func(r *Registry, c *BuildConfig) {
			o := r.Operations["documents.list"]
			o.HTTP.Query["page"] = Binding{Literal: json.RawMessage(`1`)}
			r.Operations["documents.list"] = o
		}},
		{"local schema ref missing", func(r *Registry, c *BuildConfig) {
			o := r.Operations["documents.list"]
			o.ResultSchema = json.RawMessage(`{"$ref":"#/$defs/missing"}`)
			r.Operations["documents.list"] = o
		}},
		{"overlapping upstream", func(r *Registry, c *BuildConfig) { c.Gateway.UpstreamAuthPaths = []string{"/api", "/api/documents"} }},
		{"reserved ingress", func(r *Registry, c *BuildConfig) {
			r.NativeHTTP.BasePath = "/webapi/"
			c.Gateway.IngressPath = "/webapi/"
		}},
		{"native public", func(r *Registry, c *BuildConfig) { c.Gateway.UpstreamAccess = "public" }},
		{"duplicate CLI", func(r *Registry, c *BuildConfig) {
			o := r.Operations["documents.upload"]
			o.CLI = r.Operations["documents.list"].CLI
			r.Operations["documents.upload"] = o
		}},
		{"response cap", func(r *Registry, c *BuildConfig) { r.NativeHTTP.Limits.MaxResponseBytes = 1 }},
		{"file cap", func(r *Registry, c *BuildConfig) { r.NativeHTTP.Limits.MaxTransferBytes = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r Registry
			json.Unmarshal(raw, &r)
			c := buildConfig()
			tc.mutate(&r, &c)
			b, _ := json.Marshal(r)
			if _, e := Export(b, c); e == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
}
func TestIndependentClientArtifactVectors(t *testing.T) {
	a, e := Export(fixture(t, "valid/header-page-files.json"), buildConfig())
	if e != nil {
		t.Fatal(e)
	}
	db, ib, e := a.Encode()
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name      string
		got, want []byte
	}{{"descriptor", db, fixture(t, "descriptor.json")}, {"inventory", ib, fixture(t, "inventory.json")}} {
		if !same(json.RawMessage(tc.got), json.RawMessage(tc.want)) {
			t.Errorf("%s differs from independent JavaScript vectors", tc.name)
		}
	}
	if _, e := ValidateArtifacts(fixture(t, "descriptor.json"), fixture(t, "inventory.json")); e != nil {
		t.Fatal(e)
	}
}
func TestLocalSchemaReferencesAndReadPOST(t *testing.T) {
	var r Registry
	json.Unmarshal(fixture(t, "valid/header-page-files.json"), &r)
	o := r.Operations["documents.download"]
	o.ParamsSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/id"},"output":{"type":"string","writeOnly":true}},"required":["id","output"],"$defs":{"id":{"type":"integer","minimum":1}}}`)
	r.Operations["documents.download"] = o
	b, _ := json.Marshal(r)
	if _, e := Export(b, buildConfig()); e != nil {
		t.Fatal(e)
	}
	if _, e := Export(fixture(t, "valid/post-read.json"), buildConfig()); e != nil {
		t.Fatal(e)
	}
}
