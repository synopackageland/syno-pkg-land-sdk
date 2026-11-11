package api

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const defsRef = "#/$defs/"

// schemaRefs collects the $defs names a schema value references with
// "#/$defs/<name>[/...]".
func schemaRefs(value any, into map[string]bool) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if ref, ok := item.(string); ok && key == "$ref" && strings.HasPrefix(ref, defsRef) {
				head, _, _ := strings.Cut(ref[len(defsRef):], "/")
				into[strings.ReplaceAll(strings.ReplaceAll(head, "~1", "/"), "~0", "~")] = true
				continue
			}
			schemaRefs(item, into)
		}
	case []any:
		for _, item := range v {
			schemaRefs(item, into)
		}
	}
}

// sharedClosure lists the shared definitions a schema needs: references its
// own $defs do not answer, followed transitively. A name the schema defines
// locally always resolves locally.
func sharedClosure(schema map[string]any, shared map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	local, _ := schema["$defs"].(map[string]any)
	pending := map[string]bool{}
	schemaRefs(schema, pending)
	out := map[string]json.RawMessage{}
	for len(pending) > 0 {
		next := map[string]bool{}
		for name := range pending {
			if _, ok := local[name]; ok {
				continue
			}
			if _, done := out[name]; done {
				continue
			}
			raw, ok := shared[name]
			if !ok {
				return nil, fmt.Errorf("schema reference #/$defs/%s is not defined", name)
			}
			out[name] = raw
			var value any
			_ = json.Unmarshal(raw, &value)
			schemaRefs(value, next)
		}
		pending = next
	}
	return out, nil
}

// expandSchema adds the shared definitions a schema uses to its own $defs, so
// the result is self-contained. A schema that needs none is returned as is.
func expandSchema(raw json.RawMessage, shared map[string]json.RawMessage) json.RawMessage {
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil || schema == nil {
		return raw
	}
	needed, err := sharedClosure(schema, shared)
	if err != nil || len(needed) == 0 {
		return raw
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	defs := map[string]json.RawMessage{}
	if fields["$defs"] != nil {
		_ = json.Unmarshal(fields["$defs"], &defs)
	}
	for name, def := range needed {
		defs[name] = def
	}
	fields["$defs"], _ = json.Marshal(defs)
	out, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return out
}

// validateSharedDefs checks registry-level $defs and that every operation
// schema reference resolves. A registry without $defs is not checked here,
// so earlier registries validate unchanged.
func validateSharedDefs(doc operationRegistry) error {
	if doc.Defs == nil {
		return nil
	}
	for name, raw := range doc.Defs {
		var value any
		if name == "" || json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("$defs entries must be JSON schemas: %s", name)
		}
		if _, ok := value.(map[string]any); !ok {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("$defs entries must be JSON schemas: %s", name)
			}
		}
	}
	names := make([]string, 0, len(doc.Operations))
	for name := range doc.Operations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		op := doc.Operations[name]
		for _, raw := range []json.RawMessage{op.ParamsSchema, op.ResultSchema} {
			var schema map[string]any
			if json.Unmarshal(raw, &schema) != nil || schema == nil {
				continue
			}
			if _, err := sharedClosure(schema, doc.Defs); err != nil {
				return fmt.Errorf("%v: %s", err, name)
			}
		}
	}
	return nil
}
