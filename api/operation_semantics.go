package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// OperationConsent names the data class a result carries before it reaches a
// model. Exactly one is set: a fixed Category, or Param, a JSON pointer into
// the call's params whose value names a source the App classifies at runtime.
type OperationConsent struct {
	Category string `json:"category,omitempty"`
	Param    string `json:"param,omitempty"`
}

// OperationTargets bounds how many targets one call may select. Max 0 means
// the call has no selectable targets; Traceable requires each target to be an
// explicit reference the App can resolve and audit.
type OperationTargets struct {
	Max       int  `json:"max"`
	Traceable bool `json:"traceable,omitempty"`
}

// OperationUndo is the receipt contract of a reversible change: the result
// value at From (a JSON pointer) is passed as Param to Method.
type OperationUndo struct {
	Method string `json:"method"`
	From   string `json:"from"`
	Param  string `json:"param"`
}

func strictObject(raw []byte, into any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(into)
}

func (c *OperationConsent) UnmarshalJSON(raw []byte) error {
	type plain OperationConsent
	if err := strictObject(raw, (*plain)(c)); err != nil {
		return fmt.Errorf("consent: %w", err)
	}
	return nil
}

func (t *OperationTargets) UnmarshalJSON(raw []byte) error {
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil || probe["max"] == nil {
		return fmt.Errorf("targets requires max")
	}
	type plain OperationTargets
	if err := strictObject(raw, (*plain)(t)); err != nil {
		return fmt.Errorf("targets: %w", err)
	}
	return nil
}

func (u *OperationUndo) UnmarshalJSON(raw []byte) error {
	type plain OperationUndo
	if err := strictObject(raw, (*plain)(u)); err != nil {
		return fmt.Errorf("undo: %w", err)
	}
	return nil
}

// AI classes. Tool classes are visible to a model; the others are not.
const (
	AIRead      = "read"
	AIPreview   = "preview"
	AIDraft     = "draft"
	AIPropose   = "propose"
	AILocal     = "local"
	AIOneClick  = "one-click"
	AIHumanOnly = "human-only"
	AIHidden    = "hidden"
)

// AITool reports whether the class is offered to a model as a tool.
func AITool(class string) bool {
	switch class {
	case AIRead, AIPreview, AIDraft, AIPropose, AILocal, AIOneClick:
		return true
	}
	return false
}

// AuditClass is the declared audit class, or the default: change for any
// write, read otherwise.
func (op Operation) AuditClass() string {
	if op.Audit != "" {
		return op.Audit
	}
	if op.Effect == "read" {
		return "read"
	}
	return "change"
}

// MCPExposure is the declared external MCP exposure, or the default that
// follows the AI class.
func (op Operation) MCPExposure() string {
	switch {
	case op.MCP != "":
		return op.MCP
	case AITool(op.AI):
		return "exposed"
	case op.AI == AIHumanOnly:
		return "human-only"
	}
	return "hidden"
}

// CtlExposure is the declared ctl exposure, or the default: every class
// except hidden is a ctl command.
func (op Operation) CtlExposure() string {
	switch {
	case op.CTL != "":
		return op.CTL
	case op.AI == AIHidden:
		return "hidden"
	}
	return "exposed"
}

var effectRank = map[string]int{"read": 0, "write": 1, "destructive": 2}

var (
	semanticToken = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	tierToken     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,15}$`)
	paramName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

func jsonPointer(s string) bool {
	return len(s) > 1 && len(s) <= 128 && s[0] == '/' && !strings.ContainsAny(s, "\n\r")
}

// schemaHasParam reports whether a params schema allows a top-level name. A
// schema without properties accepts any name.
func schemaHasParam(schema json.RawMessage, name string) bool {
	var value struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(schema, &value) != nil || value.Properties == nil {
		return true
	}
	_, ok := value.Properties[name]
	return ok
}

func pointerHead(pointer string) string {
	head, _, _ := strings.Cut(pointer[1:], "/")
	return strings.ReplaceAll(strings.ReplaceAll(head, "~1", "/"), "~0", "~")
}

// validateOperationClass checks the ai class against effect and the optional
// AI semantics. Errors name the field so an author can fix the spec.
func validateOperationClass(name string, op Operation, ops map[string]Operation) error {
	switch op.AI {
	case AIRead, AIPreview:
		if op.Effect != "read" {
			return fmt.Errorf("read/preview operation mutates: %s", name)
		}
	case AIDraft:
		p, ok := ops[op.Preview]
		if op.Effect == "read" || !ok || p.AI != AIPreview || p.Effect != "read" {
			return fmt.Errorf("draft requires a non-mutating preview: %s", name)
		}
	case AIPropose:
		if op.Effect != "write" {
			return fmt.Errorf("propose stores a proposal and requires effect write: %s", name)
		}
	case AILocal:
		if op.Effect != "write" {
			return fmt.Errorf("local is a reversible App-local change and requires effect write: %s", name)
		}
	case AIOneClick, AIHumanOnly, AIHidden:
	default:
		return fmt.Errorf("operation requires explicit ai class: %s", name)
	}
	if c := op.Consent; c != nil {
		switch {
		case !AITool(op.AI):
			return fmt.Errorf("consent applies only to AI tool classes: %s", name)
		case (c.Category == "") == (c.Param == ""):
			return fmt.Errorf("consent requires exactly one of category or param: %s", name)
		case c.Category != "" && !semanticToken.MatchString(c.Category):
			return fmt.Errorf("consent category must match %s: %s", semanticToken, name)
		case c.Param != "" && (!jsonPointer(c.Param) || !schemaHasParam(op.ParamsSchema, pointerHead(c.Param))):
			return fmt.Errorf("consent param must be a JSON pointer to a declared parameter: %s", name)
		}
	}
	if t := op.Targets; t != nil && (t.Max < 0 || t.Max > 1000) {
		return fmt.Errorf("targets max must be 0..1000: %s", name)
	}
	switch op.Risk {
	case "", "low", "medium", "high":
	default:
		return fmt.Errorf("risk must be low, medium or high: %s", name)
	}
	if op.Tier != "" && !tierToken.MatchString(op.Tier) {
		return fmt.Errorf("tier must match %s: %s", tierToken, name)
	}
	if u := op.Undo; u != nil {
		target, ok := ops[u.Method]
		switch {
		case op.Effect == "read":
			return fmt.Errorf("undo applies only to a change: %s", name)
		case u.Method == name || !ok || target.Effect == "read" || target.AI == AIHidden:
			return fmt.Errorf("undo method must be another visible change operation: %s", name)
		case !jsonPointer(u.From):
			return fmt.Errorf("undo from must be a JSON pointer into the result: %s", name)
		case !paramName.MatchString(u.Param) || !schemaHasParam(target.ParamsSchema, u.Param):
			return fmt.Errorf("undo param must be a parameter of %s: %s", u.Method, name)
		}
	}
	switch op.Audit {
	case "", "change", "sensitive-read":
	case "read", "none":
		if op.Effect != "read" {
			return fmt.Errorf("audit of a change must be change or sensitive-read: %s", name)
		}
	default:
		return fmt.Errorf("audit must be change, sensitive-read, read or none: %s", name)
	}
	switch op.MCP {
	case "":
	case "exposed":
		if !AITool(op.AI) {
			return fmt.Errorf("mcp exposed requires an AI tool class: %s", name)
		}
	case "human-only":
		if op.AI == AIHidden {
			return fmt.Errorf("mcp cannot expose a hidden operation: %s", name)
		}
	case "hidden":
	default:
		return fmt.Errorf("mcp must be exposed, hidden or human-only: %s", name)
	}
	switch op.CTL {
	case "", "hidden":
	case "exposed":
		if op.AI == AIHidden {
			return fmt.Errorf("ctl cannot expose a hidden operation: %s", name)
		}
	default:
		return fmt.Errorf("ctl must be exposed or hidden: %s", name)
	}
	if op.Gateway != "" {
		if err := validateGateway(name, op, ops); err != nil {
			return err
		}
	}
	if op.DynamicProvider && !AITool(op.AI) {
		return fmt.Errorf("dynamicProvider requires an AI tool class: %s", name)
	}
	if len(op.Labels) > 16 {
		return fmt.Errorf("labels allow at most 16 entries: %s", name)
	}
	for key, value := range op.Labels {
		if !semanticToken.MatchString(key) || len(value) > 128 || strings.ContainsAny(value, "\n\r") {
			return fmt.Errorf("labels need lowercase keys and one-line values up to 128 bytes: %s", name)
		}
	}
	return nil
}

// validateGateway checks that ctl reaches name through a dedicated hidden
// invoke route whose route record (permission, audit) matches the operation
// and whose execution floor is never weaker.
func validateGateway(name string, op Operation, ops map[string]Operation) error {
	g, ok := ops[op.Gateway]
	switch {
	case op.CtlExposure() != "exposed":
		return fmt.Errorf("gateway applies only to an operation exposed to ctl: %s", name)
	case op.Gateway == name || !ok:
		return fmt.Errorf("gateway must name another registered operation: %s", name)
	case g.AI != AIHidden || g.Flow.Type != "invoke" || g.Gateway != "":
		return fmt.Errorf("gateway must name a hidden invoke operation without its own gateway: %s", name)
	case g.Permission != op.Permission:
		return fmt.Errorf("gateway must name an operation with the same permission: %s", name)
	case effectRank[g.Effect] < effectRank[op.Effect]:
		return fmt.Errorf("gateway effect must be at least the operation's effect: %s", name)
	case g.AuditClass() != op.AuditClass():
		return fmt.Errorf("gateway audit must equal the operation's audit class: %s", name)
	}
	for other, spec := range ops {
		if other < name && spec.Gateway == op.Gateway {
			return fmt.Errorf("gateway serves only one operation: %s", name)
		}
	}
	return nil
}
