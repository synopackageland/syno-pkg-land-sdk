package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func testCatalog(t *testing.T) *ToolCatalog {
	t.Helper()
	raw, _, _ := semanticsRegistry(t, nil)
	catalog, err := NewToolCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

// A fake App with static operations and one runtime namespace. The runtime
// namespace is built at turn time (an enabled integration) and is never
// written into the registry; its tools inherit the carrier operation's gate,
// audit, consent and Allow-changes rules.
func TestRuntimeNamespaceCoexistsWithStaticTools(t *testing.T) {
	raw, _, _ := semanticsRegistry(t, nil)
	before := string(raw)
	catalog, err := NewToolCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	plugin := RuntimeNamespace{Name: "plugin", Description: "Rows of an enabled integration", Tools: []RuntimeTool{
		{Name: "rows", Description: "Read the integration's rows when the person asks about it", Method: "rows.read", Fixed: map[string]any{"source": "plugin.rows"}},
		{Name: "save", Description: "Save a note for this integration when the person asks", Method: "notes.save", Fixed: map[string]any{"source": "plugin"}},
	}}
	tools, err := catalog.Tools("app", plugin)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != before {
		t.Fatal("runtime namespace changed registry bytes")
	}
	byName := map[string]Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{"app__notes_list", "app__notes_save", "app__notes_propose", "app__notes_export", "app__rows_read", "plugin__rows", "plugin__save"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing tool %s in %v", name, tools)
		}
	}
	if _, ok := byName["app__notes_undo"]; ok {
		t.Fatal("human-only operation became a tool")
	}
	static, dynamic := byName["app__notes_save"], byName["plugin__save"]
	if !dynamic.Runtime || static.Runtime || dynamic.Namespace != "plugin" {
		t.Fatal("runtime marker")
	}
	for _, turn := range []Turn{{Via: ViaAI}, {Via: ViaAI, AllowChanges: true}, {Via: ViaMCP}, {Via: ViaMCP, AllowChanges: true}, {Via: ViaCtl}} {
		if static.Gate(turn) != dynamic.Gate(turn) {
			t.Fatalf("runtime tool gate differs for %+v", turn)
		}
	}
	if dynamic.AuditClass() != "change" || static.AuditClass() != dynamic.AuditClass() {
		t.Fatal("runtime tool audit differs")
	}
	if dynamic.Gate(Turn{Via: ViaAI}) != GateCard || dynamic.Gate(Turn{Via: ViaAI, AllowChanges: true}) != GateRun || dynamic.Gate(Turn{Via: ViaMCP}) != GateRefuse {
		t.Fatal("Allow-changes rule")
	}
	rows := byName["plugin__rows"]
	params, err := rows.Bind(json.RawMessage(`{"limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	consent := rows.Consent(params)
	if consent.Category != "" || consent.Source != "plugin.rows" {
		t.Fatalf("consent from fixed source: %+v", consent)
	}
	if c := byName["app__notes_list"].Consent(json.RawMessage(`{}`)); c.Category != "inventory" {
		t.Fatalf("static consent: %+v", c)
	}
	if _, err = rows.Bind(json.RawMessage(`{"source":"other"}`)); err == nil {
		t.Fatal("model overrode a fixed value")
	}
	if _, err = rows.Bind(json.RawMessage(`{"source":"plugin.rows"}`)); err != nil {
		t.Fatal("same fixed value refused")
	}
}

func TestRuntimeNamespaceRefusals(t *testing.T) {
	catalog := testCatalog(t)
	cases := []struct {
		name string
		ns   RuntimeNamespace
	}{
		{"static name", RuntimeNamespace{Name: "app", Tools: []RuntimeTool{{Name: "x", Description: "Read rows when asked", Method: "rows.read"}}}},
		{"bad name", RuntimeNamespace{Name: "Plugin X", Tools: []RuntimeTool{{Name: "x", Description: "Read rows when asked", Method: "rows.read"}}}},
		{"unknown carrier", RuntimeNamespace{Name: "plugin", Tools: []RuntimeTool{{Name: "x", Description: "Read rows when asked", Method: "rows.none"}}}},
		{"carrier without dynamicProvider", RuntimeNamespace{Name: "plugin", Tools: []RuntimeTool{{Name: "x", Description: "List notes when asked", Method: "notes.list"}}}},
		{"human-only carrier", RuntimeNamespace{Name: "plugin", Tools: []RuntimeTool{{Name: "x", Description: "Undo a note when asked", Method: "notes.undo"}}}},
		{"no description", RuntimeNamespace{Name: "plugin", Tools: []RuntimeTool{{Name: "x", Method: "rows.read"}}}},
		{"duplicate", RuntimeNamespace{Name: "plugin", Tools: []RuntimeTool{{Name: "x", Description: "Read rows when asked", Method: "rows.read"}, {Name: "x", Description: "Read rows when asked", Method: "rows.read"}}}},
		{"bad params", RuntimeNamespace{Name: "plugin", Tools: []RuntimeTool{{Name: "x", Description: "Read rows when asked", Method: "rows.read", Params: json.RawMessage(`[]`)}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := catalog.Tools("app", test.ns); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if _, err := catalog.Tools("app", RuntimeNamespace{Name: "a", Tools: []RuntimeTool{{Name: "b__c", Description: "Read rows when asked", Method: "rows.read"}}}, RuntimeNamespace{Name: "a__b", Tools: []RuntimeTool{{Name: "c", Description: "Read rows when asked", Method: "rows.read"}}}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("identifier collision: %v", err)
	}
}

func TestToolGateTable(t *testing.T) {
	catalog := testCatalog(t)
	tools, err := catalog.Tools("app")
	if err != nil {
		t.Fatal(err)
	}
	gate := map[string]Tool{}
	for _, tool := range tools {
		gate[tool.Method] = tool
	}
	cases := []struct {
		method string
		turn   Turn
		want   GateDecision
	}{
		{"notes.list", Turn{Via: ViaAI}, GateRun},
		{"notes.list", Turn{Via: ViaMCP}, GateRun},
		{"notes.propose", Turn{Via: ViaAI}, GateIntentRequired},
		{"notes.propose", Turn{Via: ViaAI, AllowChanges: true}, GatePropose},
		{"notes.propose", Turn{Via: ViaMCP}, GateRefuse},
		{"notes.save", Turn{Via: ViaAI}, GateCard},
		{"notes.save", Turn{Via: ViaAI, AllowChanges: true}, GateRun},
		{"notes.save", Turn{Via: ViaCtl, AllowChanges: true}, GateRun},
		{"notes.export", Turn{Via: ViaAI, AllowChanges: true}, GateCard},
		{"notes.export", Turn{Via: ViaMCP}, GateCard},
		{"notes.list", Turn{Via: "unknown"}, GateRefuse},
	}
	for _, c := range cases {
		if got := gate[c.method].Gate(c.turn); got != c.want {
			t.Errorf("%s %+v: got %s want %s", c.method, c.turn, got, c.want)
		}
	}
	// An MCP-hidden tool stays callable in the App's own chat only.
	raw, _, _ := semanticsRegistry(t, func(o map[string]map[string]any) { o["notes.list"]["mcp"] = "hidden" })
	hiddenCatalog, err := NewToolCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	hidden, _ := hiddenCatalog.Tools("app")
	for _, tool := range hidden {
		if tool.Method == "notes.list" && (tool.Gate(Turn{Via: ViaMCP}) != GateRefuse || tool.Gate(Turn{Via: ViaAI}) != GateRun) {
			t.Fatal("mcp hidden")
		}
	}
	draftRaw := []byte(`{"format":"synopkgland.api","version":2,"operations":{
	 "app.plan":{"summary":"Preview a change","ai":"preview","effect":"read","permission":"administrators","paramsSchema":{"type":"object"},"resultSchema":{"type":"object"},"flow":{"type":"invoke","method":"app.plan"}},
	 "app.apply":{"summary":"Apply a change","ai":"draft","preview":"app.plan","effect":"write","permission":"administrators","paramsSchema":{"type":"object"},"resultSchema":{"type":"object"},"flow":{"type":"invoke","method":"app.apply"}}}}`)
	draftCatalog, err := NewToolCatalog(draftRaw)
	if err != nil {
		t.Fatal(err)
	}
	draftTools, _ := draftCatalog.Tools("app")
	for _, tool := range draftTools {
		if tool.Method == "app.apply" && tool.Gate(Turn{Via: ViaAI, AllowChanges: true}) != GatePreview {
			t.Fatal("draft must stay preview-only")
		}
	}
}

func TestToolUndoAction(t *testing.T) {
	catalog := testCatalog(t)
	tools, _ := catalog.Tools("app")
	for _, tool := range tools {
		if tool.Method != "notes.save" {
			continue
		}
		undo, ok := tool.Undo(json.RawMessage(`{"ok":true,"undo_id":"evt-7"}`))
		if !ok || undo.Method != "notes.undo" || undo.Params["id"] != "evt-7" {
			t.Fatalf("undo %+v %v", undo, ok)
		}
		if _, ok = tool.Undo(json.RawMessage(`{"ok":true}`)); ok {
			t.Fatal("receipt without undo id offered undo")
		}
		return
	}
	t.Fatal("notes.save missing")
}

func TestToolGateCtlHidden(t *testing.T) {
	raw, _, _ := semanticsRegistry(t, withGateway)
	catalog, err := NewToolCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	tools, _ := catalog.Tools("app")
	gate := map[string]Tool{}
	for _, tool := range tools {
		gate[tool.Method] = tool
	}
	export := gate["notes.export"]
	if export.Gate(Turn{Via: ViaCtl}) != GateRefuse || export.Gate(Turn{Via: ViaAI}) != GateCard || export.Gate(Turn{Via: ViaMCP}) != GateCard {
		t.Fatal("ctl hidden must refuse ctl only")
	}
	if gate["notes.list"].Gate(Turn{Via: ViaCtl}) != GateRun || gate["notes.save"].Gate(Turn{Via: ViaCtl, AllowChanges: true}) != GateRun {
		t.Fatal("gatewayed ops keep their ctl gate")
	}
	if gate["notes.list"].Gateway != "ctl.notes.list" || gate["rows.read"].Gateway != "" {
		t.Fatal("tool gateway")
	}
}

func TestToolCatalogForGateway(t *testing.T) {
	raw, _, _ := semanticsRegistry(t, withGateway)
	catalog, err := NewToolCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := catalog.ForGateway("ctl.notes.save")
	if !ok || tool.Method != "notes.save" || tool.Gateway != "ctl.notes.save" || tool.Operation.AI != AILocal || tool.AuditClass() != "change" {
		t.Fatalf("served tool %+v %v", tool, ok)
	}
	if tool.Gate(Turn{Via: ViaCtl}) != GateRefuse || tool.Gate(Turn{Via: ViaCtl, AllowChanges: true}) != GateRun {
		t.Fatal("served gate")
	}
	if undo, ok := tool.Undo(json.RawMessage(`{"undo_id":"e1"}`)); !ok || undo.Method != "notes.undo" {
		t.Fatal("served undo")
	}
	if tool.Consent(nil).Category != "inventory" {
		t.Fatal("served consent")
	}
	for _, unknown := range []string{"ctl.notes.none", "notes.list", ""} {
		if _, ok := catalog.ForGateway(unknown); ok {
			t.Fatalf("unknown gateway %q found", unknown)
		}
	}
	// notes.save is a dynamicProvider with a gateway: runtime tools inherit it.
	tools, err := catalog.Tools("app", RuntimeNamespace{Name: "plugin", Tools: []RuntimeTool{{Name: "save", Description: "Save a plugin note when asked", Method: "notes.save", Fixed: map[string]any{"source": "plugin"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if last := tools[len(tools)-1]; !last.Runtime || last.Gateway != "ctl.notes.save" {
		t.Fatalf("runtime tool gateway %+v", last)
	}
}

// A registry without the ctl fields or shared $defs gives the same catalog
// as before: every tool's params are the operation's bytes and no tool has a
// gateway; ctl gates as AI.
func TestToolCatalogWithoutCtlFieldsUnchanged(t *testing.T) {
	raw, _, _ := semanticsRegistry(t, nil)
	var doc operationRegistry
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewToolCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	tools, _ := catalog.Tools("app")
	for _, tool := range tools {
		if string(tool.Params) != string(doc.Operations[tool.Method].ParamsSchema) || tool.Gateway != "" {
			t.Fatalf("%s changed", tool.Method)
		}
		for _, allow := range []bool{false, true} {
			ctl, ai := tool.Gate(Turn{Via: ViaCtl, AllowChanges: allow}), tool.Gate(Turn{Via: ViaAI, AllowChanges: allow})
			if ctl != ai && !(ai == GateIntentRequired || ai == GateCard) {
				t.Fatalf("%s ctl %s ai %s", tool.Method, ctl, ai)
			}
		}
	}
}
