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
| `propose` | write | Stores a proposal (for example a draft change) when the person switched on Allow changes; a person reviews and applies it |
| `local` | write | A reversible change to the App's own state. The model always gets a card the person presses, with or without Allow changes |
| `one-click` | any | Never runs on a model call; the model gets a card the person presses |
| `human-only` | any | Not a tool. ctl with normal confirmation unless `ctl: hidden` |
| `hidden` | any | Not a tool, not a ctl command |

No AI write runs without a person's click. On the AI and MCP channels
`Tool.Gate` never returns `run` for a write: it returns `card`, `propose` or
`preview`, and the write runs only after a person confirms it (see
[Write approval](#write-approval)). Allow changes is an input the App binds
from its own UI; it only lets the model propose changes.

`propose`, `local` and `one-click` need a per-turn decision that ctl does not
have, so `ctl tool` returns a dry-run plan for them and never executes.

## Gate decisions

`tool.Gate(api.Turn{Via, AllowChanges})`:

| `ai` | AI chat | AI chat, Allow changes | MCP | MCP, Allow changes | ctl |
| --- | --- | --- | --- | --- | --- |
| `read`, `preview` | run | run | run | run | run |
| `draft` | preview | preview | preview | preview | preview |
| `propose` | intent-required | propose | refuse | propose | refuse |
| `local` | card | card | refuse | card | run with AllowChanges, else refuse |
| `one-click` | card | card | card | card | card |

MCP rows apply when the operation's `mcp` is `exposed`; `human-only` gives
`card` and `hidden` gives `refuse`. ctl refuses an operation with `ctl: hidden`.

## Write approval

The SDK holds the approval contract; each App keeps its own UI (an Allow
changes switch, cards or both) and its own storage. Every `card`, `propose`
and `preview` decision becomes an `api.AIProposal`:

```go
turn := api.Turn{Via: api.ViaAI, AllowChanges: allow, ConversationID: conv, TurnID: turnID, ToolCallID: call.ID}
params, err := tool.Bind(call.Args)
p, err := tool.Propose(turn, caller.User, params, time.Now(), 0) // 0: DefaultProposalTTL (10 min); at most MaxProposalTTL (15 min)
p.PreviewDigest, err = api.DigestValue(preview)                 // what the person will see, if any
// store p; audit p.AuditEvent(now); give the model p.ID and the App's card

// The person's click, on the server:
err = p.Confirm(api.AIConfirmation{Principal: caller.User, ParamsDigest: sent.ParamsDigest, PreviewDigest: sent.PreviewDigest}, time.Now())
// ErrProposalExpired (p is now expired; save it), ErrProposalPrincipal,
// ErrProposalDigest or ErrProposalClosed refuse the click.
// Save p (state uncertain) durably, dispatch, then:
err = p.Finish(api.ProposalSucceeded, "Saved note n1", "", receipt, time.Now())
```

| Field | Meaning |
| --- | --- |
| `id` | 32 lowercase hex characters from a random source |
| `principal` | The person whose turn it is; only they can confirm or cancel |
| `method`, `via`, `decision` | The operation, the channel and the gate decision |
| `params_digest` | `DigestJSON` of the bound params |
| `preview_digest` | `DigestValue` of what the person is shown; empty when none |
| `allow_changes`, `conversation_id`, `turn_id`, `tool_call_id` | The turn's binding |
| `created_at`, `expires_at` | `expires_at` is checked by the server in `Confirm`, never only by the page |
| `state` | `awaiting-human`, `uncertain`, `accepted`, `succeeded`, `partial`, `failed`, `canceled`, `expired`, `superseded` |
| `receipt` | `proposal_id`, `state`, `confirmed_by`, `confirmed_at`, `finished_at`, `code`, `summary`, `result` |

- `Confirm` checks state, expiry at the server's clock, principal, params
  digest and preview digest, then moves the proposal to `uncertain`. Save it
  before dispatch. After a crash, look an `uncertain` proposal's outcome up;
  never dispatch it again.
- `Finish` records `accepted` (a job still runs), then `succeeded`, `partial`
  or `failed`. `Cancel(principal, now)` and `Supersede(now)` close an
  awaiting proposal; `Expire(now)` closes one past `expires_at` (for a sweep).
- `Receipt.ForModel()` gives `proposal_id`, `state`, `summary` and `code` for
  an App that reports the outcome back to the model. `result` and
  `confirmed_by` stay in the App.
- `Removable(now, retention)` says when an App may delete a stored proposal:
  it ended (or expired unconfirmed) at least `retention` ago; 0 is
  `DefaultProposalRetention` (30 days). `uncertain` and `accepted` proposals
  are never removable.
- `DigestJSON` is the lowercase hex SHA-256 of canonical JSON (sorted keys, no
  insignificant space, numbers as written); empty params digest as `{}`.

## AI audit events

`api.AIAuditEvent` is the shared audit format; the App writes it to its own
audit store (`Fields()` gives a map with the JSON names).

- `tool.AuditCall(turn, params, now)`: event `ai.tool` for one model call,
  with the gate `decision` and `audit_class`.
- `p.AuditEvent(now)`: event `ai.proposal` with the proposal's `state`; record
  one when the proposal is created and after every state change. `actor` is
  the person who confirmed.

Both carry `proposed_by: "ai"` (AI and MCP channels), `via`, `method`,
`allow_changes` (always present), `conversation_id`, `turn_id`,
`tool_call_id` and `params_digest`; proposal events add `proposal_id`. Put the
`proposal_id` on the App's own business audit entry of the confirmed write too,
so the write traces back to the AI turn.

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
// propose, card, intent-required or refuse (a write is never run on a model
// call; see Write approval); tool.Consent(params),
// tool.AuditClass() and tool.Undo(result) come from the carrier.
```

`Tools` refuses a runtime tool whose carrier is missing, not an AI tool, or not
a `dynamicProvider`, and any two tools whose model names collide. Recompute
the runtime namespaces every turn; a cached tool list must still pass the App's
per-call authorization and target checks.
