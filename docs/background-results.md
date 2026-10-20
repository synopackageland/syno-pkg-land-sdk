# Structured background jobs

Use `background.Store` and `Runner.Work` to move long App operations out of a
request. The package account owns the private spool; no new service or root
permission is required. The App must actually run its worker independently of
the CGI request. Submitting a job alone does not make an existing handler
asynchronous.

## Submit and run

Construct the owner from fresh, verified DSM identity, never from JSON parameters.
Validate inputs in the App and dispatch only fixed, trusted operations in Work.
Use `NewRequestStore` in request handlers for bounded lock waits and `NewStore`
in the worker. Both must use the same private directory.

```go
requestStore := background.NewRequestStore(spool)
admission, err := requestStore.SubmitSingleton(ctx, "search", background.SubmitRequest{
    Key: requestKey, Name: "Search", Data: validatedJSON,
    Submitter: background.Submitter{User: verifiedUsername, UID: verifiedUID},
})
// Return admission promptly. Do not run Work in the request.
```

The first submission returns `outcome: "accepted"` and `job.id`. Another pending
job in the same owner/scope returns `outcome: "busy"` with the existing ID, without
accepting the new input. Queued, running and paused jobs occupy their scope. The
check and admission share the existing journal lock, including across processes.
Other owners have independent scopes and never receive another owner's ID.
Global resource admission remains the App's policy; this is an owner-scoped lock.

An idempotency key remains bound to input and singleton scope until the job is
trimmed or purged. Reusing it with different input/scope returns `ErrKeyConflict`.
Completed-key reuse returns the original job with `duplicate: true` and
`outcome: "accepted"`, including its original terminal state; use a new key to
start another run. On a lost response, query the original ID if known, or retry
the original key/input/scope. Do not blindly submit with a new key.
`Submit` without a singleton retains its existing duplicate behavior.

Configure the existing Runner's Work in the package worker assembly:

```go
runner := background.NewRunner(spool, workerInfo)
runner.Timeout = 2 * time.Minute // Choose an App execution budget, independently of HTTP.
runner.Work = func(ctx context.Context, task *background.Task) (background.Result, error) {
    task.Report(background.Progress{Phase: "searching", Done: 0, Total: 0})
    // Decode task.Input and call the App's context-aware, fixed operation.
    rows, err := search(ctx, task.Input)
    if err != nil { return background.Result{}, err }
    raw, err := json.Marshal(rows)
    return background.Result{JSON: raw}, err
}
err = runner.Run(packageContext)
```

The default Work is still SHA256V1. Custom Work is a trusted Go assembly choice;
the legacy `worker`/`algorithm` metadata remains `sha256-v1` for this slot, rather
than identifying App operations. Use validated App inputs to dispatch multiple
fixed operations. Changing Work's behavior/input/checkpoint contract across
upgrades requires an App migration policy; it must not reinterpret queued work.
The bridge's `bg.submit` remains a fixed hash operation, not a generic cross-language
worker API. Go Apps may also inject Work into their existing `delegate.Service`.

The runner verifies a bounded snapshot of the private input before calling Work;
Work consumes that same snapshot. Structured results do not have to return the
input's hash. Legacy hash results remain `{sha256, bytes}` and keep their existing
validation. Check the context during I/O and between work units: cancellation and
timeouts are cooperative. The default job timeout is 60 seconds; changing request
timeouts does not extend it.

## Result reads and limits

`Result.JSON` is an optional `json.RawMessage`. Any JSON value, including `null`,
is supported. The encoded JSON limit is **16 KiB**, including HTML escaping;
UTF-8 must be valid and nesting must meet `jsonlimits.MaxDepth` (32). The entire
encoded Result, including any hash receipt, is bounded to 16 KiB + 512 bytes.
Invalid output finishes as `failed` / `result-invalid`; oversized output finishes
as `failed` / `result-too-large`. It cannot leave the job running and block the
queue. Failed, canceled, interrupted and paused work publishes no result.

```go
access := background.Access{Owner: verifiedOwner, Admin: verifiedAdministrator}
raw, err := requestStore.ResultFor(ctx, id, access) // Return the JSON value, not a quoted string.
page, err := requestStore.ResultPageFor(ctx, id, access, offset, limit)
```

`ResultFor` returns `ErrResultUnavailable` until successful structured output
exists, including for a successful legacy hash-only job. Missing, hidden and
unauthorized jobs all return `ErrNotFound`. Identity matches both username and
UID; unresolved legacy owners are visible only to administrators. Build Access
from fresh authentication on **every** status/result/page/cancel request.
App routes must enforce Application Privileges, their own product permissions,
and safe result schemas/redaction in addition to this store ownership rule.

`ResultPageFor` accepts top-level arrays only. Its response has `items`, `offset`,
`total`, and an optional `nextOffset`. Limits are 1–100 items; offsets range from
zero through total (the latter returns an empty array). Invalid bounds and
non-array pagination return `ErrInvalid` after the ownership check. Pages are
stable because successful results are immutable. Use `ResultFor` for objects
and scalars. Large datasets need a separate App-owned, bounded artifact API
with fresh authorization, referenced by an opaque ID; do not store paths,
credentials or raw private provider configuration in job results.

## Progress, cancellation and retention

`Task.Report` accepts `Progress.Phase`, an App-defined UTF-8 label bounded to 64
bytes without splitting a code point. Existing done/total/bytes/item fields and
throttled publication remain. Leave unknown totals unknown; no percentage or ETA
is inferred. Use `Task.Checkpoint` only for work safe to repeat after a pause.

Use `CancelFor` with fresh Access. Queued/paused jobs cancel immediately; a running
job observes its context. A cancel committed before Finish wins over success,
including invalid output. Cancellation does not undo completed external effects.
A package stop pauses work for the existing checkpoint/resume mechanism; worker
crashes interrupt running jobs without replay. Product-specific certification,
privileged execution, approvals and recovery rules remain the App's responsibility.

Pending jobs reserve the maximum result size in existing owner/total spool
quotas. Finish releases unused space and charges the encoded result size with a
512-byte minimum receipt allowance. Retained input copies still count separately.
Results stay with the newest 64 terminal jobs, subject to those quotas, with no
time-based retention promise. History trimming and the existing administrative
`PurgeOwner` remove results in the same durable journal commit; running purges
stay hidden until cancellation drains. Result reads do not extend retention.

The journal writes schema 3 and reads schemas 1 and 2. Older writers refuse
schema 3 to preserve structured results and singleton state; do not downgrade
onto an active schema-3 spool. Package stop/recovery and worker revision checks
retain their existing semantics.

## ctl and root jobs

App-owned APIs can return this minimal admission shape:

```json
{"outcome":"accepted","job":{"id":"<original-id>","state":"queued"},"duplicate":false}
```

Expose only capabilities the App actually implements: a prompt start response,
status by the original ID, authorized result reads and optional array pages or
cancellation. API names are App-owned. Project status responses rather than
returning complete results on every poll. Map all terminal failures, including
`canceled` and `interrupted`; `paused` is not success.

Existing ctl job flow already supports structured JSON results. Configure
`start`, `poll`, optional `result`, `jobId: "job.id"`, `idParam: "id"`, `state`,
and the App's actual `done`/`failed` states. `--no-wait` invokes start once and
returns its response under `job`; its outer `accepted: true` means that the start
call returned, so inspect `job.outcome` to distinguish a new request from busy.
For AI commands requiring an explicit nonblocking admission response, prefer an
`invoke` descriptor and separate existing read commands for status/result.
A wait timeout leaves the original job running; query it rather than resubmit.
The existing paginate flow can consume `items` with offset/limit and a page size
at most 100. Apply explicit result schemas to redact sensitive App fields.
No CLI core change is required for these shapes.

Executor `root.jobs.*` already has bounded JSON results and phase progress. Its
protected root-program store, administrator/session checks, job IDs, state names,
cancellation and retention differ from package-account background jobs. Keep the
two existing persistence and permission boundaries; normalize at the App adapter
only when useful. Do not run an approved root operation in package-account Work
or persist a DSM session to reuse it later. Privileged planning still needs an
actual authorized asynchronous App API before ctl can wrap it. Adding this SDK
result capability alone does not migrate any App operation or publish a release.
