package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// ToolCatalog is the AI view of a trusted v2 operation registry. It holds
// metadata only: the App runtime still authenticates, authorizes, audits and
// dispatches every call through its registered handlers.
type ToolCatalog struct {
	ops map[string]Operation
}

// NewToolCatalog validates the registry's operation metadata. Route equality
// is checked separately by ValidateOperationRegistry at server construction.
func NewToolCatalog(registry []byte) (*ToolCatalog, error) {
	var doc operationRegistry
	if err := json.Unmarshal(registry, &doc); err != nil {
		return nil, fmt.Errorf("invalid operation registry: %w", err)
	}
	if doc.Format != "synopkgland.api" || doc.Version != 2 || doc.Operations == nil {
		return nil, fmt.Errorf("expected synopkgland.api v2 operations")
	}
	for name, op := range doc.Operations {
		if err := validateOperationClass(name, op, doc.Operations); err != nil {
			return nil, err
		}
	}
	return &ToolCatalog{ops: doc.Operations}, nil
}

// Operation returns one operation's metadata.
func (c *ToolCatalog) Operation(name string) (Operation, bool) {
	op, ok := c.ops[name]
	return op, ok
}

// RuntimeNamespace is a group of tools the App builds at turn time, for
// example from the integrations an administrator enabled. It never enters the
// static registry. Each tool runs a registered carrier operation declared
// with dynamicProvider with fixed
// parameters, so it inherits that operation's gate, consent, audit and
// Allow-changes rules and cannot widen them.
type RuntimeNamespace struct {
	Name        string
	Description string
	Tools       []RuntimeTool
}

// RuntimeTool binds a model-facing tool to a registered carrier Method.
// Params is the model's parameter schema (nil uses the carrier's); Fixed
// values are merged over the model's arguments and cannot be overridden.
type RuntimeTool struct {
	Name        string
	Description string
	Params      json.RawMessage
	Method      string
	Fixed       map[string]any
}

// Tool is one model-facing tool: a static operation or a runtime binding.
type Tool struct {
	Name        string // namespace__tool, as a model writes it
	Namespace   string
	Description string
	Params      json.RawMessage
	Method      string
	Fixed       map[string]any
	Operation   Operation
	Runtime     bool
}

var namespaceName = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// ToolIdentifier is a name as scripts and models write it.
func ToolIdentifier(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

// Tools lists the static AI tool operations under namespace, followed by
// the runtime namespaces. Names must stay unique after identifier mapping.
func (c *ToolCatalog) Tools(namespace string, runtime ...RuntimeNamespace) ([]Tool, error) {
	if !namespaceName.MatchString(namespace) {
		return nil, fmt.Errorf("invalid tool namespace %q", namespace)
	}
	names := make([]string, 0, len(c.ops))
	for name, op := range c.ops {
		if AITool(op.AI) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := []Tool{}
	seen := map[string]bool{}
	add := func(tool Tool) error {
		if seen[tool.Name] {
			return fmt.Errorf("tool %s is defined twice", tool.Name)
		}
		seen[tool.Name] = true
		out = append(out, tool)
		return nil
	}
	for _, name := range names {
		op := c.ops[name]
		if err := add(Tool{Name: ToolIdentifier(namespace) + "__" + ToolIdentifier(name), Namespace: namespace, Description: op.Summary, Params: op.ParamsSchema, Method: name, Operation: op}); err != nil {
			return nil, err
		}
	}
	for _, ns := range runtime {
		if ns.Name == namespace || !namespaceName.MatchString(ns.Name) {
			return nil, fmt.Errorf("runtime namespace %q must be a distinct lowercase name", ns.Name)
		}
		for _, rt := range ns.Tools {
			op, ok := c.ops[rt.Method]
			if !ok || !AITool(op.AI) || !op.DynamicProvider {
				return nil, fmt.Errorf("runtime tool %s.%s needs a registered AI tool operation with dynamicProvider as carrier", ns.Name, rt.Name)
			}
			if !namespaceName.MatchString(rt.Name) || strings.TrimSpace(rt.Description) == "" {
				return nil, fmt.Errorf("runtime tool %s.%s needs a lowercase name and a description", ns.Name, rt.Name)
			}
			params := rt.Params
			if params == nil {
				params = op.ParamsSchema
			}
			if !schemaObject(params) {
				return nil, fmt.Errorf("runtime tool %s.%s needs an object parameter schema", ns.Name, rt.Name)
			}
			if err := add(Tool{Name: ToolIdentifier(ns.Name) + "__" + ToolIdentifier(rt.Name), Namespace: ns.Name, Description: rt.Description, Params: params, Method: rt.Method, Fixed: rt.Fixed, Operation: op, Runtime: true}); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Bind merges the tool's fixed values over the model's arguments. A model
// argument may repeat a fixed value but never change it.
func (t Tool) Bind(args json.RawMessage) (json.RawMessage, error) {
	params := map[string]any{}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &params); err != nil || params == nil {
			return nil, fmt.Errorf("tool arguments must be a JSON object")
		}
	}
	for key, fixed := range t.Fixed {
		if value, ok := params[key]; ok && !sameJSON(value, fixed) {
			return nil, fmt.Errorf("tool argument %s is fixed", key)
		}
		params[key] = fixed
	}
	return json.Marshal(params)
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	var u, v any
	_ = json.Unmarshal(x, &u)
	_ = json.Unmarshal(y, &v)
	return reflect.DeepEqual(u, v)
}

// Caller channels.
const (
	ViaAI  = "ai"  // the App's own model turn
	ViaMCP = "mcp" // an external MCP client
	ViaCtl = "ctl" // a person through ctl
)

// Turn is what the server bound to this call: its channel and whether the
// person switched on Allow changes for this turn. Nothing in a model's call
// can set either.
type Turn struct {
	Via          string
	AllowChanges bool
}

// GateDecision tells the App runtime what to do with one tool call.
type GateDecision string

const (
	GateRun            GateDecision = "run"             // dispatch the handler
	GatePreview        GateDecision = "preview"         // run the named preview only; a person confirms
	GatePropose        GateDecision = "propose"         // App draft gate, then store the proposal
	GateCard           GateDecision = "card"            // return a card the person presses
	GateIntentRequired GateDecision = "intent-required" // ask the person to allow changes
	GateRefuse         GateDecision = "refuse"
)

// Gate applies the metadata rules. The App still runs its authorization,
// draft gate, target checks and audit after a run or propose decision.
func (t Tool) Gate(turn Turn) GateDecision {
	op := t.Operation
	switch turn.Via {
	case ViaAI, ViaCtl:
	case ViaMCP:
		switch op.MCPExposure() {
		case "exposed":
		case "human-only":
			return GateCard
		default:
			return GateRefuse
		}
	default:
		return GateRefuse
	}
	switch op.AI {
	case AIRead, AIPreview:
		return GateRun
	case AIDraft:
		return GatePreview
	case AIPropose:
		if turn.AllowChanges {
			return GatePropose
		}
		if turn.Via == ViaAI {
			return GateIntentRequired
		}
	case AILocal:
		if turn.AllowChanges {
			return GateRun
		}
		if turn.Via == ViaAI {
			return GateCard
		}
	case AIOneClick:
		return GateCard
	}
	return GateRefuse
}

// AuditClass is the carrier operation's audit class.
func (t Tool) AuditClass() string { return t.Operation.AuditClass() }

// Consent is what a bound call needs before its result reaches a model:
// a fixed Category, or the Source reference the App classifies.
type Consent struct {
	Category string
	Source   string
}

// Consent resolves the declaration against bound params (see Bind).
func (t Tool) Consent(params json.RawMessage) Consent {
	c := t.Operation.Consent
	if c == nil {
		return Consent{}
	}
	if c.Category != "" {
		return Consent{Category: c.Category}
	}
	ref, _ := pointerValue(params, c.Param).(string)
	return Consent{Source: ref}
}

// UndoAction is the call that reverses a receipt.
type UndoAction struct {
	Method string
	Params map[string]any
}

// Undo reads the receipt's undo reference. ok is false when the operation
// declares no undo or this result carries no reference.
func (t Tool) Undo(result json.RawMessage) (UndoAction, bool) {
	u := t.Operation.Undo
	if u == nil {
		return UndoAction{}, false
	}
	value := pointerValue(result, u.From)
	if value == nil || value == "" {
		return UndoAction{}, false
	}
	return UndoAction{Method: u.Method, Params: map[string]any{u.Param: value}}, true
}

func pointerValue(raw json.RawMessage, pointer string) any {
	var v any
	if pointer == "" || json.Unmarshal(raw, &v) != nil {
		return nil
	}
	for _, key := range strings.Split(pointer[1:], "/") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")]
	}
	return v
}
