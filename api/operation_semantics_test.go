package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// semanticsRegistry is one App with every v2 AI class and semantic field.
func semanticsRegistry(t *testing.T, mutate func(map[string]map[string]any)) ([]byte, []byte, []Route) {
	t.Helper()
	op := func(name, ai, effect string) map[string]any {
		return map[string]any{
			"summary": "Change " + name + " when the person asks for it", "ai": ai, "effect": effect, "permission": "administrators",
			"paramsSchema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}, "source": map[string]any{"type": "string"}}},
			"resultSchema": map[string]any{"type": "object"},
			"flow":         map[string]any{"type": "invoke", "method": name},
		}
	}
	ops := map[string]map[string]any{
		"notes.list":    op("notes.list", "read", "read"),
		"notes.save":    op("notes.save", "local", "write"),
		"notes.undo":    op("notes.undo", "human-only", "write"),
		"notes.propose": op("notes.propose", "propose", "write"),
		"notes.export":  op("notes.export", "one-click", "read"),
		"rows.read":     op("rows.read", "read", "read"),
	}
	ops["notes.list"]["consent"] = map[string]any{"category": "inventory"}
	ops["notes.list"]["targets"] = map[string]any{"max": 20, "traceable": true}
	ops["notes.list"]["risk"] = "low"
	ops["notes.list"]["tier"] = "R"
	ops["notes.list"]["labels"] = map[string]any{"group": "notes"}
	ops["notes.save"]["undo"] = map[string]any{"method": "notes.undo", "from": "/undo_id", "param": "id"}
	ops["notes.save"]["consent"] = map[string]any{"category": "inventory"}
	ops["notes.export"]["audit"] = "sensitive-read"
	ops["notes.export"]["mcp"] = "human-only"
	ops["rows.read"]["consent"] = map[string]any{"param": "/source"}
	ops["rows.read"]["dynamicProvider"] = true
	ops["notes.save"]["dynamicProvider"] = true
	if mutate != nil {
		mutate(ops)
	}
	raw, err := json.Marshal(map[string]any{"format": "synopkgland.api", "version": 2, "operations": ops})
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]string{}
	var routes []Route
	for name := range ops {
		methods[name] = "administrators"
		routes = append(routes, Handle(name, func(context.Context, Caller, json.RawMessage) (any, error) { panic("metadata must not run handlers") }))
	}
	permissions, _ := json.Marshal(map[string]any{"schema": 1, "default": "administrators", "methods": methods})
	return raw, permissions, routes
}

func TestOperationSemanticsAccepted(t *testing.T) {
	raw, permissions, routes := semanticsRegistry(t, nil)
	if err := ValidateOperationRegistry(raw, permissions, routes...); err != nil {
		t.Fatal(err)
	}
}

func TestOperationSemanticsRejected(t *testing.T) {
	cases := []struct {
		name   string
		want   string
		mutate func(map[string]map[string]any)
	}{
		{"propose must write", "propose", func(o map[string]map[string]any) { o["notes.propose"]["effect"] = "read" }},
		{"propose cannot be destructive", "propose", func(o map[string]map[string]any) { o["notes.propose"]["effect"] = "destructive" }},
		{"local must write", "local", func(o map[string]map[string]any) { o["notes.save"]["effect"] = "destructive" }},
		{"consent needs one source", "consent", func(o map[string]map[string]any) {
			o["notes.list"]["consent"] = map[string]any{"category": "inventory", "param": "/source"}
		}},
		{"consent empty", "consent", func(o map[string]map[string]any) { o["notes.list"]["consent"] = map[string]any{} }},
		{"consent category token", "consent", func(o map[string]map[string]any) {
			o["notes.list"]["consent"] = map[string]any{"category": "Inventory Data"}
		}},
		{"consent param pointer", "consent", func(o map[string]map[string]any) { o["rows.read"]["consent"] = map[string]any{"param": "source"} }},
		{"consent param unknown", "consent", func(o map[string]map[string]any) { o["rows.read"]["consent"] = map[string]any{"param": "/missing"} }},
		{"consent on human-only", "consent", func(o map[string]map[string]any) {
			o["notes.undo"]["consent"] = map[string]any{"category": "inventory"}
		}},
		{"consent unknown key", "consent", func(o map[string]map[string]any) {
			o["notes.list"]["consent"] = map[string]any{"category": "x", "extra": true}
		}},
		{"targets range", "targets", func(o map[string]map[string]any) { o["notes.list"]["targets"] = map[string]any{"max": 1001} }},
		{"targets negative", "targets", func(o map[string]map[string]any) { o["notes.list"]["targets"] = map[string]any{"max": -1} }},
		{"targets missing max", "targets", func(o map[string]map[string]any) { o["notes.list"]["targets"] = map[string]any{"traceable": true} }},
		{"risk enum", "risk", func(o map[string]map[string]any) { o["notes.list"]["risk"] = "critical" }},
		{"tier token", "tier", func(o map[string]map[string]any) { o["notes.list"]["tier"] = "tier one" }},
		{"undo method missing", "undo", func(o map[string]map[string]any) { o["notes.save"]["undo"].(map[string]any)["method"] = "notes.none" }},
		{"undo self", "undo", func(o map[string]map[string]any) { o["notes.save"]["undo"].(map[string]any)["method"] = "notes.save" }},
		{"undo read method", "undo", func(o map[string]map[string]any) { o["notes.save"]["undo"].(map[string]any)["method"] = "notes.list" }},
		{"undo hidden method", "undo", func(o map[string]map[string]any) { o["notes.undo"]["ai"] = "hidden" }},
		{"undo on read", "undo", func(o map[string]map[string]any) {
			o["notes.list"]["undo"] = map[string]any{"method": "notes.undo", "from": "/undo_id", "param": "id"}
		}},
		{"undo pointer", "undo", func(o map[string]map[string]any) { o["notes.save"]["undo"].(map[string]any)["from"] = "undo_id" }},
		{"undo param unknown", "undo", func(o map[string]map[string]any) { o["notes.save"]["undo"].(map[string]any)["param"] = "event" }},
		{"audit enum", "audit", func(o map[string]map[string]any) { o["notes.list"]["audit"] = "verbose" }},
		{"audit change downgrade", "audit", func(o map[string]map[string]any) { o["notes.save"]["audit"] = "read" }},
		{"audit none on write", "audit", func(o map[string]map[string]any) { o["notes.save"]["audit"] = "none" }},
		{"mcp enum", "mcp", func(o map[string]map[string]any) { o["notes.list"]["mcp"] = "public" }},
		{"mcp cannot widen human-only", "mcp", func(o map[string]map[string]any) { o["notes.undo"]["mcp"] = "exposed" }},
		{"mcp cannot widen hidden", "mcp", func(o map[string]map[string]any) {
			o["rows.read"]["ai"] = "hidden"
			delete(o["rows.read"], "consent")
			o["rows.read"]["mcp"] = "human-only"
		}},
		{"dynamicProvider on human-only", "dynamicProvider", func(o map[string]map[string]any) { o["notes.undo"]["dynamicProvider"] = true }},
		{"labels key", "labels", func(o map[string]map[string]any) { o["notes.list"]["labels"] = map[string]any{"Group": "notes"} }},
		{"labels value", "labels", func(o map[string]map[string]any) { o["notes.list"]["labels"] = map[string]any{"group": "a\nb"} }},
		{"labels count", "labels", func(o map[string]map[string]any) {
			labels := map[string]any{}
			for _, k := range strings.Split("a b c d e f g h i j k l m n o p q", " ") {
				labels[k] = "x"
			}
			o["notes.list"]["labels"] = labels
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			raw, permissions, routes := semanticsRegistry(t, test.mutate)
			err := ValidateOperationRegistry(raw, permissions, routes...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error naming %q", err, test.want)
			}
		})
	}
}

// Every v2 registry written before these fields keeps validating unchanged,
// and its effective defaults follow from ai and effect alone.
func TestOperationSemanticsDefaults(t *testing.T) {
	raw := operationSource("read")
	permissions := []byte(`{"schema":1,"default":"administrators","methods":{"app.status":"authenticated"}}`)
	if err := ValidateOperationRegistry(raw, permissions, Handle("app.status", func(context.Context, Caller, json.RawMessage) (any, error) { return nil, nil })); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ ai, effect, audit, mcp string }{
		{"read", "read", "read", "exposed"},
		{"draft", "write", "change", "exposed"},
		{"local", "write", "change", "exposed"},
		{"one-click", "destructive", "change", "exposed"},
		{"human-only", "write", "change", "human-only"},
		{"hidden", "read", "read", "hidden"},
	} {
		op := Operation{AI: c.ai, Effect: c.effect}
		if op.AuditClass() != c.audit || op.MCPExposure() != c.mcp {
			t.Fatalf("%s/%s: audit %s mcp %s", c.ai, c.effect, op.AuditClass(), op.MCPExposure())
		}
	}
}
