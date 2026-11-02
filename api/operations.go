package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Operation is build-time metadata. Handlers and the SDK continue to own
// authentication and resource authorization; metadata grants no caller authority.
type Operation struct {
	Summary      string          `json:"summary"`
	AI           string          `json:"ai"`
	Effect       string          `json:"effect"`
	Permission   string          `json:"permission"`
	ParamsSchema json.RawMessage `json:"paramsSchema"`
	ResultSchema json.RawMessage `json:"resultSchema"`
	Flow         struct {
		Type   string `json:"type"`
		Method string `json:"method"`
		Start  string `json:"start"`
		Poll   string `json:"poll"`
		Result string `json:"result"`
	} `json:"flow"`
	Preview     string `json:"preview,omitempty"`
	LongRunning bool   `json:"longRunning,omitempty"`
}

type operationRegistry struct {
	Format     string               `json:"format"`
	Version    int                  `json:"version"`
	Operations map[string]Operation `json:"operations"`
}

func infrastructureOperation(name string) bool {
	switch name {
	case DescriptionMethod, "identity.status", "identity.login", "identity.session", "identity.logout":
		return true
	}
	return false
}

// ValidateOperationRegistry checks equality in both directions, including hidden
// operations and SDK capability routes. Only the fixed discovery/session routes
// are infrastructure. It runs before handlers, migrations or network I/O.
func ValidateOperationRegistry(raw, permissions []byte, routes ...Route) error {
	if len(raw) > MaxRequestBytes {
		return fmt.Errorf("operation registry exceeds request limit")
	}
	var doc operationRegistry
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("invalid operation registry: %w", err)
	}
	if doc.Format != "synopkgland.api" || doc.Version != 2 || doc.Operations == nil || len(doc.Operations) > MaxPermissionMethods {
		return fmt.Errorf("expected synopkgland.api v2 operations")
	}
	actual, err := DescribeRoutes(permissions, routes...)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, route := range actual {
		name := route["method"]
		seen[name] = true
		if infrastructureOperation(name) {
			continue
		}
		op, ok := doc.Operations[name]
		if !ok {
			return fmt.Errorf("registered handler has no operation spec: %s", name)
		}
		if op.Permission != route["permission"] {
			return fmt.Errorf("operation permission differs from handler: %s", name)
		}
	}
	for name, op := range doc.Operations {
		if infrastructureOperation(name) || !seen[name] {
			return fmt.Errorf("operation spec has no business handler: %s", name)
		}
		if strings.TrimSpace(op.Summary) == "" || !schemaObject(op.ParamsSchema) || !schemaObject(op.ResultSchema) {
			return fmt.Errorf("operation requires summary and params/result schemas: %s", name)
		}
		switch op.Effect {
		case "read", "write", "destructive":
		default:
			return fmt.Errorf("invalid operation effect: %s", name)
		}
		if op.Permission == IntegrationPermission && op.AI != "hidden" {
			return fmt.Errorf("integration routes must be hidden: %s", name)
		}
		switch op.AI {
		case "read", "preview":
			if op.Effect != "read" {
				return fmt.Errorf("read/preview operation mutates: %s", name)
			}
		case "draft":
			p, ok := doc.Operations[op.Preview]
			if op.Effect == "read" || !ok || p.AI != "preview" || p.Effect != "read" {
				return fmt.Errorf("draft requires a non-mutating preview: %s", name)
			}
		case "human-only", "hidden":
		default:
			return fmt.Errorf("operation requires explicit ai class: %s", name)
		}
		first := op.Flow.Method
		if op.Flow.Type == "job" {
			first = op.Flow.Start
		}
		if first != name {
			return fmt.Errorf("flow must start at its registered method: %s", name)
		}
		switch op.Flow.Type {
		case "invoke", "paginate", "ndjson", "upload":
		case "job":
			for index, helper := range []string{op.Flow.Poll, op.Flow.Result} {
				if index == 1 && helper == "" {
					continue
				}
				spec, ok := doc.Operations[helper]
				if !ok || spec.Effect != "read" {
					return fmt.Errorf("job helper requires read operation: %s", name)
				}
			}
		default:
			return fmt.Errorf("invalid operation flow: %s", name)
		}
		if op.LongRunning && op.Flow.Type != "job" {
			return fmt.Errorf("long operation requires job flow: %s", name)
		}
	}
	return nil
}
func schemaObject(raw json.RawMessage) bool {
	var value map[string]any
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil && value != nil
}

// NewServerWithOperations adds registry equality to the existing permission and
// transport guards. registry and permissions must be trusted embedded build data.
func NewServerWithOperations(auth Authenticator, registry, permissions []byte, routes ...Route) (*Server, error) {
	if err := ValidateOperationRegistry(registry, permissions, routes...); err != nil {
		return nil, err
	}
	return NewServerWithPermissions(auth, permissions, routes...)
}
