package api

import (
	"context"
	"encoding/json"
	"os"
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
		methods[name] = ops[name]["permission"].(string)
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

// withGateway routes notes.list (read) and notes.save (local write) through
// dedicated hidden ctl gateways and keeps notes.export off ctl.
func withGateway(o map[string]map[string]any) {
	gateway := func(name, effect string) map[string]any {
		return map[string]any{
			"summary": "ctl gateway: runs the served operation through the audited engine", "ai": "hidden", "effect": effect, "permission": "administrators",
			"paramsSchema": map[string]any{"type": "object", "required": []any{"scope"}, "properties": map[string]any{"scope": map[string]any{"type": "string"}}},
			"resultSchema": map[string]any{"type": "object"},
			"flow":         map[string]any{"type": "invoke", "method": name},
		}
	}
	o["ctl.notes.list"] = gateway("ctl.notes.list", "read")
	o["ctl.notes.save"] = gateway("ctl.notes.save", "write")
	o["notes.list"]["gateway"] = "ctl.notes.list"
	o["notes.save"]["gateway"] = "ctl.notes.save"
	o["notes.export"]["ctl"] = "hidden"
}

func TestOperationGatewayAccepted(t *testing.T) {
	raw, permissions, routes := semanticsRegistry(t, withGateway)
	if err := ValidateOperationRegistry(raw, permissions, routes...); err != nil {
		t.Fatal(err)
	}
}

func TestOperationGatewayRejected(t *testing.T) {
	cases := []struct {
		name, want string
		mutate     func(map[string]map[string]any)
	}{
		{"ctl enum", "ctl must be exposed or hidden", func(o map[string]map[string]any) { o["notes.list"]["ctl"] = "public" }},
		{"ctl exposed on hidden", "ctl cannot expose a hidden operation", func(o map[string]map[string]any) { o["ctl.notes.list"]["ctl"] = "exposed" }},
		{"gateway missing", "gateway must name another registered operation", func(o map[string]map[string]any) { o["notes.list"]["gateway"] = "ctl.notes.none" }},
		{"gateway itself", "gateway must name another registered operation", func(o map[string]map[string]any) { o["notes.list"]["gateway"] = "notes.list" }},
		{"gateway not hidden", "gateway must name a hidden invoke operation", func(o map[string]map[string]any) { o["ctl.notes.list"]["ai"] = "human-only" }},
		{"gateway not invoke", "gateway must name a hidden invoke operation", func(o map[string]map[string]any) {
			o["ctl.notes.list"]["flow"] = map[string]any{"type": "ndjson", "method": "ctl.notes.list"}
		}},
		{"gateway has its own gateway", "gateway", func(o map[string]map[string]any) {
			o["ctl.notes.list"]["gateway"] = "notes.list"
		}},
		{"gateway permission differs", "gateway must name an operation with the same permission", func(o map[string]map[string]any) {
			o["ctl.notes.list"]["permission"] = "authenticated"
		}},
		{"gateway weaker effect", "gateway effect must be at least the operation's effect", func(o map[string]map[string]any) { o["ctl.notes.save"]["effect"] = "read" }},
		{"gateway audit differs", "gateway audit must equal the operation's audit class", func(o map[string]map[string]any) {
			o["ctl.notes.list"]["audit"] = "sensitive-read"
		}},
		{"gateway shared by two", "gateway serves only one operation", func(o map[string]map[string]any) { o["rows.read"]["gateway"] = "ctl.notes.list" }},
		{"gateway on ctl hidden", "gateway applies only to an operation exposed to ctl", func(o map[string]map[string]any) { o["notes.list"]["ctl"] = "hidden" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			raw, permissions, routes := semanticsRegistry(t, func(o map[string]map[string]any) { withGateway(o); test.mutate(o) })
			err := ValidateOperationRegistry(raw, permissions, routes...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %q", err, test.want)
			}
			if _, err := NewToolCatalog(raw); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("catalog got %v, want %q", err, test.want)
			}
		})
	}
}

// Rule A: an export or reveal keeps a write execution floor but is audited
// as sensitive-read; read and none stay refused for changes.
func TestOperationAuditSensitiveReadOnChange(t *testing.T) {
	for _, effect := range []string{"write", "destructive"} {
		raw, permissions, routes := semanticsRegistry(t, func(o map[string]map[string]any) {
			o["notes.undo"]["effect"] = effect
			o["notes.undo"]["audit"] = "sensitive-read"
		})
		if err := ValidateOperationRegistry(raw, permissions, routes...); err != nil {
			t.Fatalf("%s: %v", effect, err)
		}
		for _, audit := range []string{"read", "none"} {
			raw, permissions, routes := semanticsRegistry(t, func(o map[string]map[string]any) {
				o["notes.undo"]["effect"] = effect
				o["notes.undo"]["audit"] = audit
			})
			if err := ValidateOperationRegistry(raw, permissions, routes...); err == nil || !strings.Contains(err.Error(), "audit of a change must be change or sensitive-read") {
				t.Fatalf("%s/%s: %v", effect, audit, err)
			}
		}
	}
}

func TestOperationCtlExposureDefaults(t *testing.T) {
	for _, c := range []struct{ ai, ctl, want string }{
		{"read", "", "exposed"}, {"preview", "", "exposed"}, {"draft", "", "exposed"}, {"propose", "", "exposed"},
		{"local", "", "exposed"}, {"one-click", "", "exposed"}, {"human-only", "", "exposed"}, {"hidden", "", "hidden"},
		{"read", "hidden", "hidden"}, {"human-only", "hidden", "hidden"}, {"read", "exposed", "exposed"},
	} {
		if got := (Operation{AI: c.ai, CTL: c.ctl}).CtlExposure(); got != c.want {
			t.Fatalf("%s/%q: %s", c.ai, c.ctl, got)
		}
	}
}

func sharedDefsRegistry(shared string, params string) []byte {
	return []byte(`{"format":"synopkgland.api","version":2` + shared + `,"operations":{
	 "app.list":{"summary":"List items when the person asks for them","ai":"read","effect":"read","permission":"administrators","paramsSchema":` + params + `,"resultSchema":{"type":"object","properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Item"}}}},"flow":{"type":"invoke","method":"app.list"}}}}`)
}

func TestOperationSharedDefs(t *testing.T) {
	params := `{"type":"object","properties":{"filter":{"$ref":"#/$defs/Filter"},"local":{"$ref":"#/$defs/Local"}},"$defs":{"Local":{"type":"string"}}}`
	raw := sharedDefsRegistry(`,"$defs":{"Filter":{"type":"object","properties":{"value":{"$ref":"#/$defs/Value"}}},"Value":{"type":"string"},"Item":{"type":"object"},"Unused":{"type":"null"}}`, params)
	catalog, err := NewToolCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	tools, _ := catalog.Tools("app")
	var schema struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(tools[0].Params, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Defs) != 3 || schema.Defs["Filter"] == nil || schema.Defs["Value"] == nil || schema.Defs["Local"] == nil {
		t.Fatalf("expanded params %s", tools[0].Params)
	}
	// A self-contained schema is passed through byte for byte.
	plain := `{"type":"object","properties":{"local":{"$ref":"#/$defs/Local"}},"$defs":{"Local":{"type":"string"}}}`
	catalog, _ = NewToolCatalog(sharedDefsRegistry(`,"$defs":{"Item":{"type":"object"}}`, plain))
	tools, _ = catalog.Tools("app")
	if string(tools[0].Params) != plain {
		t.Fatalf("self-contained params changed: %s", tools[0].Params)
	}
	for _, bad := range []string{`,"$defs":{"Item":{"type":"object"}}`, `,"$defs":{"Filter":{},"Value":{},"Item":[]}`} {
		if _, err := NewToolCatalog(sharedDefsRegistry(bad, params)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	if _, err := NewToolCatalog(sharedDefsRegistry(`,"$defs":{"Item":{"type":"object"}}`, params)); err == nil || !strings.Contains(err.Error(), "schema reference #/$defs/Filter is not defined: app.list") {
		t.Fatalf("dangling ref: %v", err)
	}
}

// A gateway that declares its own gateway is refused even before the
// gateway's own validation reports it.
func TestOperationGatewayChainRefused(t *testing.T) {
	ops := map[string]Operation{
		"notes.list":     {AI: AIRead, Effect: "read", Permission: "administrators", Gateway: "ctl.notes.list"},
		"ctl.notes.list": {AI: AIHidden, Effect: "read", Permission: "administrators", Gateway: "notes.list"},
	}
	ops["ctl.notes.list"] = func(op Operation) Operation { op.Flow.Type = "invoke"; return op }(ops["ctl.notes.list"])
	if err := validateGateway("notes.list", ops["notes.list"], ops); err == nil || err.Error() != "gateway must name a hidden invoke operation without its own gateway: notes.list" {
		t.Fatal(err)
	}
}

// The CLI validates the same fixture and must report the same messages.
func TestOperationCtlGatewayParityFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/ctl-gateway-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Registry map[string]any `json:"registry"`
		Cases    []struct {
			Name      string                    `json:"name"`
			Patch     map[string]map[string]any `json:"patch"`
			Operation string                    `json:"operation"`
			Message   string                    `json:"message"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var registry map[string]any
			base, _ := json.Marshal(fixture.Registry)
			_ = json.Unmarshal(base, &registry)
			ops := registry["operations"].(map[string]any)
			for name, fields := range c.Patch {
				for key, value := range fields {
					ops[name].(map[string]any)[key] = value
				}
			}
			patched, _ := json.Marshal(registry)
			_, err := NewToolCatalog(patched)
			want := ""
			if c.Message != "" {
				want = c.Message + ": " + c.Operation
			}
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}
