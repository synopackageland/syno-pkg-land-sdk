package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func operationSource(ai string) []byte {
	return []byte(`{"format":"synopkgland.api","version":2,"operations":{"app.status":{"summary":"Read App status","ai":"` + ai + `","effect":"read","permission":"authenticated","paramsSchema":{"type":"object"},"resultSchema":{"type":"object"},"flow":{"type":"invoke","method":"app.status"}}}}`)
}
func TestOperationRegistryRequiresExplicitClassAndExactRoutes(t *testing.T) {
	permissions := []byte(`{"schema":1,"default":"administrators","methods":{"app.status":"authenticated"}}`)
	calls := 0
	handler := func(context.Context, Caller, json.RawMessage) (any, error) { calls++; return nil, nil }
	routes := []Route{Handle("app.status", handler)}
	if err := ValidateOperationRegistry(operationSource("read"), permissions, routes...); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		raw    []byte
		routes []Route
	}{
		{"missing class", operationSource(""), routes},
		{"unknown class", operationSource("automatic"), routes},
		{"handler without spec", operationSource("read"), append(routes, Handle("app.other", handler))},
		{"spec without handler", operationSource("read"), nil},
		{"permission mismatch", []byte(strings.ReplaceAll(string(operationSource("read")), `"permission":"authenticated"`, `"permission":"administrators"`)), routes},
		{"hidden still requires handler", operationSource("hidden"), nil},
		{"draft without preview", operationSource("draft"), routes},
		{"long synchronous operation", []byte(strings.Replace(string(operationSource("read")), `"effect":"read"`, `"longRunning":true,"effect":"read"`, 1)), routes},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateOperationRegistry(test.raw, permissions, test.routes...); err == nil {
				t.Fatal("accepted invalid registry")
			}
		})
	}
	if calls != 0 {
		t.Fatal("registry validation executed a handler")
	}
}
func TestOperationRegistryHiddenHandlerIsValid(t *testing.T) {
	permissions := []byte(`{"schema":1,"default":"administrators","methods":{"app.status":"authenticated"}}`)
	if err := ValidateOperationRegistry(operationSource("hidden"), permissions, Handle("app.status", func(context.Context, Caller, json.RawMessage) (any, error) { panic("must not run") })); err != nil {
		t.Fatal(err)
	}
}

func TestSDKOperationCatalogCoversEveryPolicyMethod(t *testing.T) {
	raw, err := os.ReadFile("operations.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc operationRegistry
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var groups map[string]map[string]string
	if err = json.Unmarshal(sdkPermissionsJSON, &groups); err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		for name, permission := range group {
			if infrastructureOperation(name) {
				continue
			}
			op, ok := doc.Operations[name]
			if !ok || op.Permission != permission || op.AI != "hidden" || op.Summary == "" || !schemaObject(op.ParamsSchema) || !schemaObject(op.ResultSchema) {
				t.Fatalf("SDK policy has no hidden operation spec: %s", name)
			}
		}
	}
}
