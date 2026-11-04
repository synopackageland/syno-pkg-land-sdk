package bridge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDescribeRuntimeRoutesUsesActualCapabilityRoutes(t *testing.T) {
	raw := []byte(`{"methods":["app.status"],"capabilities":{"portal":true}}`)
	permissions := []byte(`{"schema":1,"default":"administrators","methods":{"app.status":"authenticated"}}`)
	for _, executor := range []bool{false, true} {
		routes, err := DescribeRuntimeRoutes(raw, permissions, executor)
		if err != nil {
			t.Fatal(err)
		}
		methods := map[string]bool{}
		for _, route := range routes {
			methods[route["method"]] = true
		}
		if !methods["app.status"] || !methods["portal.status"] || methods["portal.register"] != executor || methods["portal.unregister"] != executor {
			t.Fatalf("incorrect runtime route set: %v", methods)
		}
	}
}
func TestRuntimeRegistryDigestAndEqualityBeforeAppExecution(t *testing.T) {
	manifest := `{"schema":1,"runtime":"python","entry":"main.py","mode":"exec","methods":["app.status"]}`
	permissions := `{"schema":1,"default":"administrators","methods":{"app.status":"authenticated"}}`
	dir := appDir(t, manifest, permissions, nil)
	registry := []byte(`{"format":"synopkgland.api","version":2,"operations":{"app.status":{"summary":"Read status","ai":"hidden","effect":"read","permission":"authenticated","paramsSchema":{},"resultSchema":{},"flow":{"type":"invoke","method":"app.status"}}}}`)
	path := filepath.Join(dir, "synopkgland-operations.json")
	if err := os.WriteFile(path, registry, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{PackageID: "Example", AppDir: dir, DataDir: t.TempDir(), Auth: userAuth{api.Identity{User: "caller"}}, OperationsDigest: registryDigest(registry)}
	if _, err := NewRuntime(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(registry, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntime(cfg); err == nil {
		t.Fatal("accepted modified runtime registry")
	}
	var value map[string]any
	if err := json.Unmarshal(registry, &value); err != nil {
		t.Fatal(err)
	}
	value["operations"] = map[string]any{}
	registry, _ = json.Marshal(value)
	os.WriteFile(path, registry, 0600)
	cfg.OperationsDigest = registryDigest(registry)
	if _, err := NewRuntime(cfg); err == nil {
		t.Fatal("accepted handler without spec")
	}
}

func registryDigest(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

func TestLargeRuntimeRegistryLoadsWithDigestAndExactRoutes(t *testing.T) {
	operations := map[string]any{}
	permissions := map[string]string{}
	methods := []string{}
	for i := range 556 {
		name := fmt.Sprintf("catalog.item%d.get", i)
		ai := "hidden"
		if i < 228 {
			ai = "read"
		}
		operations[name] = map[string]any{"summary": "Read object", "ai": ai, "effect": "read", "permission": "authenticated", "paramsSchema": map[string]any{"type": "object"}, "resultSchema": map[string]any{"type": "object", "description": strings.Repeat("Model documentation ", 200)}, "flow": map[string]any{"type": "invoke", "method": name}}
		methods = append(methods, name)
		permissions[name] = "authenticated"
	}
	raw, err := json.Marshal(map[string]any{"format": "synopkgland.api", "version": 2, "operations": operations})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= api.MaxRequestBytes {
		t.Fatal("fixture must exceed former metadata cap")
	}
	manifest, _ := json.Marshal(map[string]any{"schema": 1, "runtime": "python", "entry": "main.py", "mode": "exec", "methods": methods})
	policy, _ := json.Marshal(map[string]any{"schema": 1, "default": "administrators", "methods": permissions})
	dir := appDir(t, string(manifest), string(policy), nil)
	path := filepath.Join(dir, "synopkgland-operations.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{PackageID: "Example", AppDir: dir, DataDir: t.TempDir(), Auth: userAuth{api.Identity{User: "caller"}}, OperationsDigest: registryDigest(raw)}
	if _, err := NewRuntime(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntime(cfg); err == nil {
		t.Fatal("large registry bypassed digest verification")
	}
}
