# AI operation semantics

A `synopkgland.api` v2 operation can describe how an AI assistant, an external
MCP client and ctl may use it. Every field below is optional. A registry that
declares none of them validates and generates exactly as before.

The fields are metadata. They grant no authority: the App still authenticates,
authorizes, checks targets, runs its draft gate and audits every call through
its registered handler. Validation runs in the SDK (`ValidateOperationRegistry`,
`NewToolCatalog`) and in `synopkgland ctl generate` / `synopkgland check`.

## AI classes

| `ai` | Effect | What the model gets |
| --- | --- | --- |
| `read` | read | Runs the fixed flow |
| `preview` | read | Runs the fixed flow; a draft names it |
| `draft` | write, destructive | Runs the named preview only; a person confirms the write |
| `propose` | write | Stores a proposal (for example a draft change) when the turn allows changes; a person reviews and applies it |
| `local` | write | A reversible change to the App's own state. Runs when the person switched on Allow changes for this turn; otherwise the model gets a card the person presses |
| `one-click` | any | Never runs on a model call; the model gets a card the person presses |
| `human-only` | any | Not a tool. ctl with normal confirmation unless `ctl: hidden` |
| `hidden` | any | Not a tool, not a ctl command |

`propose`, `local` and `one-click` need a per-turn decision that ctl does not
have, so `ctl tool` returns a dry-run plan for them and never executes.

## Fields

| Field | Shape | Rule |
| --- | --- | --- |
| `consent` | `{"category": "inventory"}` or `{"param": "/source"}` | Data class of the result before it reaches a model. `category` is a lowercase token the App defines (for example `inventory`, `logs`, `public`). `param` is a JSON pointer into params; the App classifies the named source at runtime. Exactly one. Only on AI tool classes |
| `targets` | `{"max": 20, "traceable": true}` | `max` 0..1000 targets per call; 0 means no selectable targets. `traceable` requires explicit references the App resolves and audits |
| `risk` | `low`, `medium`, `high` | Shown to people and models |
| `tier` | short token, for example `R` or `T2` | App-defined confirmation tier; the SDK carries it unchanged |
| `undo` | `{"method": "event.undo", "from": "/undo_event_id", "param": "id"}` | Receipt contract of a reversible change: the result value at `from` is passed as `param` to `method`, another visible write operation |
| `audit` | `change`, `sensitive-read`, `read`, `none` | Default: `change` for writes, `read` otherwise. A write is `change` or `sensitive-read` (for example an export that keeps a write execution floor but writes its own access record); `read` and `none` are refused for writes |
| `mcp` | `exposed`, `hidden`, `human-only` | External MCP exposure. Default follows `ai`: tool classes `exposed`, `human-only` stays human-only, `hidden` stays hidden. It can only narrow: `human-only` lists the tool as a card, `hidden` removes it |
| `ctl` | `exposed`, `hidden` | ctl exposure. Default: `exposed` for every class except `hidden`. It can only narrow: `hidden` keeps an operation, for example a browser intent or a human-only stream, off ctl |
| `gateway` | operation name, for example `"ctl.notes.get"` | ctl and external callers reach this operation through the named gateway operation instead of its own route (see below) |
| `dynamicProvider` | `true` | The App binds runtime tools to this operation (see below). Generators note that its tool list is completed at runtime. Only on AI tool classes |
| `labels` | `{"group": "devices"}` | Up to 16 App-defined one-line strings, carried unchanged. Never use them for authorization |

Chat exposure is the AI class; ctl exposure is `ctl`; MCP exposure is `mcp`. Generated `tools.json` entries add these fields only when an
operation declares them, so existing readers keep working.

## ctl gateways

Some Apps do not let ctl call business routes directly: ctl reaches an audited
tool engine through one gateway route per tool, so ctl follows the same rules
as the App's own AI (a required scope, `via=ctl` attribution, the App's change
policy). The served operation keeps its business schemas, AI class and
semantics and names the gateway:

```json
"notes.get": {
  "summary": "Read one note by reference when the person asks about it",
  "ai": "read", "effect": "read", "permission": "administrators",
  "cli": ["notes", "get"], "flow": {"type": "invoke", "method": "notes.get"},
  "paramsSchema": {"...": "business params"}, "resultSchema": {"...": "business result"},
  "gateway": "ctl.notes.get"
},
"ctl.notes.get": {
  "summary": "ctl gateway: runs notes.get through the audited tool engine",
  "ai": "hidden", "effect": "read", "permission": "administrators",
  "cli": ["ctl", "notes", "get"], "flow": {"type": "invoke", "method": "ctl.notes.get"},
  "paramsSchema": {"...": "what an external caller sends"}, "resultSchema": {"...": "the engine's result"}
}
```

The gateway is an ordinary registered operation with its own handler. It must
be `hidden` with an `invoke` flow and no gateway of its own, have the same
permission and audit class as the served operation and at least its effect
(read < write < destructive), and serve exactly one operation. Only an
operation exposed to ctl can name a gateway. The CLI also requires a JSON RPC
route. The ctl command keeps the served operation's name and CLI path but calls
the gateway with the gateway's params; generated `tools.json` keeps the business
schema, and the external MCP view shows the gateway's.

In the gateway handler, `catalog.ForGateway(method)` returns the served
operation's tool, so `Gate`, `Consent`, `AuditClass` and `Undo` come from the
served operation. `Tool.Gateway` names the gateway; runtime tools inherit their
carrier's. `Gate(Turn{Via: ViaCtl})` refuses an operation with `ctl: hidden`.

## Shared schema definitions

A large registry may declare `$defs` next to `operations`. An operation
schema's `"$ref": "#/$defs/<name>"` resolves to the schema's own `$defs` first,
then to the shared ones; every reference must resolve. `NewToolCatalog` gives
self-contained tool parameter schemas, and the CLI moves identical
per-operation definitions into the shared set for the descriptor and the
runtime registry. A registry without top-level `$defs` is never rewritten.

## Runtime namespaces

Some tools exist only at runtime, for example one tool per integration an
administrator enabled. They never enter the static registry. Each one binds to
a registered carrier operation declared with `dynamicProvider: true` and fixed
parameters, so it inherits the carrier's gate, consent, audit, targets and
Allow-changes rules and cannot widen them.

```go
catalog, err := api.NewToolCatalog(operationsJSON) // the embedded generated registry
tools, err := catalog.Tools("app", api.RuntimeNamespace{
	Name: "backup", Description: "Rows of the enabled backup integration",
	Tools: []api.RuntimeTool{{
		Name: "jobs", Description: "List backup jobs and their last result when the person asks about backups",
		Method: "source.rows", Fixed: map[string]any{"source": "backup.jobs"},
	}},
})
// Per call: tool.Bind(args) merges fixed values (a model cannot change them),
// tool.Gate(api.Turn{Via: api.ViaAI, AllowChanges: allow}) says run, preview,
// propose, card, intent-required or refuse; tool.Consent(params),
// tool.AuditClass() and tool.Undo(result) come from the carrier.
```

`Tools` refuses a runtime tool whose carrier is missing, not an AI tool, or not
a `dynamicProvider`, and any two tools whose model names collide. Recompute
the runtime namespaces every turn; a cached tool list must still pass the App's
per-call authorization and target checks.
