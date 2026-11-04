package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func largeOperationMetadata(t *testing.T) ([]byte, []byte, []Route) {
	t.Helper()
	operations := map[string]any{}
	methods := map[string]string{}
	var routes []Route
	for i := range 556 {
		name := fmt.Sprintf("catalog.item%d.get", i)
		ai := "hidden"
		if i < 228 {
			ai = "read"
		}
		operations[name] = map[string]any{
			"summary": "Read application object", "ai": ai, "effect": "read", "permission": "authenticated",
			"paramsSchema": map[string]any{"type": "object"},
			"resultSchema": map[string]any{"type": "object", "description": strings.Repeat("Public model documentation ", 160)},
			"flow":         map[string]any{"type": "invoke", "method": name},
		}
		methods[name] = "authenticated"
		routes = append(routes, Handle(name, func(context.Context, Caller, json.RawMessage) (any, error) {
			panic("metadata validation must not invoke handlers")
		}))
	}
	raw, err := json.Marshal(map[string]any{"format": "synopkgland.api", "version": 2, "operations": operations})
	if err != nil {
		t.Fatal(err)
	}
	permissions, err := json.Marshal(map[string]any{"schema": 1, "default": "administrators", "methods": methods})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= MaxRequestBytes {
		t.Fatal("fixture must exceed the former metadata cap")
	}
	return raw, permissions, routes
}

func TestLargeOperationRegistryLoadsWithoutRequestBodyCap(t *testing.T) {
	raw, permissions, routes := largeOperationMetadata(t)
	if err := ValidateOperationRegistry(raw, permissions, routes...); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServerWithOperations(&descriptionAuth{}, raw, permissions, routes...); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationRegistry(raw, permissions, routes[:len(routes)-1]...); err == nil {
		t.Fatal("large registry bypassed route equality")
	}
}

func TestLargeDescriptionFileReadsCompleteMetadataAfterAuthentication(t *testing.T) {
	raw, _, _ := largeOperationMetadata(t)
	file := filepath.Join(t.TempDir(), DescriptionFile)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a := &descriptionAuth{id: Identity{User: "fixtureuser1"}}
	h := WithDescription(http.NotFoundHandler(), a, file)
	r := httptest.NewRequest("POST", "http://nas/api", strings.NewReader(`{"method":"synopkgland.describe","params":{}}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://nas")
	r.Header.Set("Cookie", "id=session")
	r.Header.Set("X-SYNO-TOKEN", "token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var response struct {
		Success bool
		Data    json.RawMessage
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success {
		t.Fatalf("description refused: %s", w.Body.String())
	}
	var got, want any
	if err := json.Unmarshal(response.Data, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	actual, _ := json.Marshal(got)
	expected, _ := json.Marshal(want)
	if string(actual) != string(expected) || a.calls != 1 {
		t.Fatal("metadata truncated, changed or authentication bypassed")
	}
}
