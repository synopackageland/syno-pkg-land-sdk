# Slow NAS work and request deadlines

The default SDK request allowance is five minutes. The Go API, bridge runtime,
daemon JSON ingress and headless TS client use the same request contract in
`spec/protocol/v1/timeouts.json`: 300 seconds of work, with the browser waiting
two seconds longer. Preserve explicit App settings and earlier parent deadlines.
This is a finite cooperative work budget, not an end-to-end availability promise.

Long or unpredictable NAS operations should use the existing package-private
background spool and worker. A larger request allowance is useful for bounded
synchronous operations; it does not make a CGI request durable.

## One operation policy

`internal/runtimebudget.Operation` is the only default work allowance: five
minutes. Operation code must use it (or the TS `DEFAULT_REQUEST_TIMEOUT`), honor
an explicit caller/approved policy deadline, or wait without a deadline while
honoring cancellation. Authentication, directory lookup, DSM WebAPI, File Station,
notifications, registry checks, subprocesses, cleanup, uploads, downloads and
background jobs do not invent shorter local defaults. Package-account jobs can
use an explicit trusted `Runner.Timeout`. Root jobs and asynchronous moves have
no elapsed lifetime cap: they end on exit, cancellation or lifecycle/authority failure.

Bridge interpreter discovery and hello share the request allowance, without a
half-budget cold-start cutoff. The standalone Session default is the same
operation allowance. An explicit `Config.StartTimeout` remains supported.

Executor ordinary commands, JSON/raw WebAPI, lifecycle defaults and batch work
use the shared allowance. Explicit pinned root program limits remain effective;
`root.run` defaults to 300 seconds and accepts approved values up to 3600.
Socket clients leave reply reserves, but an earlier request deadline still wins.
Byte ceilings, operation allowlists, identities, grants, and no-replay rules
remain enforced. Raw byte-size hints no longer select another default timeout.

Compose gateways start while the upstream becomes ready. New ingress remains
closed until a fresh probe succeeds. Slow or missing listeners keep the package
running and the gateway showing starting; availability alone never stops the
owned child, even beyond a startup/recovery window. Invalid policy, binding,
installed version or lifecycle generation still refuses admission immediately.
Explicit lifecycle stop and cancellation remain effective.

Non-failure timing is separate: retry/backoff spacing, progress and heartbeat
polls, credential/cache lifetimes, incomplete-header framing, response reserves,
and graceful stop/reap timing after cancellation. Journal lock contention returns
retryable busy before work is admitted. Portal's five-second refresh hint keeps
the pending mutation busy and awaited. None is a second NAS work deadline.

Run `node scripts/check-timeout-policy.mjs` (also part of `scripts/gate.sh`).
It rejects literal clocks outside the canonical budget unless the exact statement
has a reviewed B exception and reason in `scripts/timeout-allowlist.json`.
Import-free browser/Python deployment copies are checked against
`spec/protocol/v1/timeouts.json`; changed literals fail the gate.

## App deadlines on a busy NAS

Do not put short per-call deadlines on DSM or executor reads. Use the default
operation allowance, or an explicit allowance measured in minutes, never seconds.
Slow calls are normal on a busy NAS: a single privileged `synowebapi --exec` can
take several seconds, with account and administrator checks adding subprocesses
before the requested work starts. A five-second read budget can expire during
those checks. Keep cancellation, and show queued/running or "Still working" while
the caller's request remains active.

### Reply reserves are per hop

For a single Go delegate socket call with an explicit parent deadline, let
`I(t) = max(0, t - min(5 seconds, t / 4))`. The layers are:

| Layer | Allowance from a 125-second parent, before elapsed work |
| --- | --- |
| App's parent context | 125 seconds |
| Client socket wait: `I(parent remaining)` | 120 seconds; leaves time to answer the App |
| Wire `deadline_ms`: `I(socket remaining)` | exactly 115000 ms; leaves time for the executor reply |
| DSM command: `budget.Inner(executor remaining)` | 110 seconds; leaves time for command cleanup and executor processing |

`executor.Connection` accepts `deadline_ms` without applying another reserve;
`commandTimeout` / `streamCommandTimeout` apply the third reserve when starting
a DSM subprocess. These protect separate replies, so no hop reserves the same
reply twice. The wire allowance is truncated to whole milliseconds. The same
calculation on a five-second parent sends 2812 ms on the wire and leaves at most
about 2.109 seconds for the next DSM command, before elapsed work.

To allow **N seconds of DSM command work**, when each layer has at least twenty
seconds remaining, give the App at least **N + 15 seconds, plus time for queueing,
backoff, authentication and earlier calls**. For example, 120 seconds of command
work needs at least 135 seconds at the App before that overhead. The executor's
approved operation policy must also allow at least N + 5 seconds, and an explicit
`Client.Timeout` must allow at least N + 10 seconds. Every earlier deadline and
approved program/operation limit still wins; increasing the parent alone cannot
raise those limits. Sequential DSM commands share the same operation deadline.
For short budgets, use the formula above rather than subtracting fifteen seconds.

`root.run` has the two socket-client reserves and its approved program timeout;
it does not apply the DSM-command reserve to the root program itself. A request
without a parent deadline omits `deadline_ms`, retaining the executor policy and
the client's ordinary socket allowance. Root batches use their existing shared
batch deadline and step count; they do not send a new per-step `deadline_ms`.
The table describes a single socket call, including a fresh attempt after a
refused batch step.

### Shared admission and safe busy retry

Go `delegate.Client.Call` queues `root.run`, `root.start` and JSON/raw
`webapi.call` by executor socket within the App process. Separately constructed
clients share the queue. Its active capacity is generated from the executor's
own root/WebAPI slot constant, currently two. Apps can remove their own two-slot
semaphores and `ROOT_BUSY` backoff loops. Status, job inspection and cancellation
bypass the queue so waiting work cannot block those control calls.

Only an explicit `ROOT_BUSY` refusal is retried automatically: the root program
or WebAPI operation has not executed. Busy retry uses increasing spacing from
100 ms up to one second, with no separate total wait cap. Queueing and backoff
stop at the caller's context deadline or cancellation. Use the ordinary request
context rather than creating a short deadline for each queued read. Separate
processes and long-lived root jobs still contend at the executor; that contention
is handled by the same refusal retry. A successful `root.start` releases local
admission after its reply; the executor continues to own the running job's slot.

A refused root batch step retries as a fresh authenticated socket request, keeping
the original batch's step count available for later steps. Every attempt uses the
same effective deadline, so time spent waiting is never restored. A generic protocol
`retryable` flag is insufficient proof of safe replay: `ROOT_TIMEOUT`,
`DSM_FAILED`, storage failures and uncertain results can follow execution.
Transport failures, lost replies, malformed replies and `WEBAPI_UNCERTAIN` are
terminal for this call and are never replayed by this client.

The TS frontend and Node/Python backends use the Go bridge for executor services;
they do not have a direct executor socket client needing a separate queue.
CLI-generated Apps should use this same request context and minute-scale
allowance.

### Work allowance after local admission

Set `Client.WorkBudget` to give each `Call` a work allowance starting when its
shared local admission slot is acquired:

```go
client := delegate.Local(packageID)
client.WorkBudget = 5 * time.Minute
err := client.Call(ctx, "webapi.call", auth, params, &result)
```

Configure the client before sharing it across goroutines, as with `Timeout`.
The queue waits only on `ctx`; its wait does not consume `WorkBudget`. After
admission the effective deadline is the earlier of the caller's deadline and
admission time plus `WorkBudget`. Operations that bypass the queue start their
budget immediately. Dial/startup waits, authentication, busy backoff and retries
consume this one budget; retries never reset it. Cancellation remains effective
throughout. A caller deadline already spent in the queue is never extended.
Zero or negative `WorkBudget` retains the existing deadline behavior.

The same per-hop reserves above apply: a 125-second budget sends 115000 ms
before elapsed work and allows at most 110 seconds for a DSM command. For
N seconds of command work in the long-budget range, allow at least N + 15 seconds
plus post-admission overhead. Earlier `ctx` deadlines, explicit socket timeouts
and approved executor policies still win. Apps can remove their own semaphore
and use an ordinary cancellable context with a minute-scale `WorkBudget`; an
outer request/proxy deadline still limits the entire wait and operation.

Budgeted calls inside an API batch use fresh authenticated socket connections
with individual `deadline_ms`, rather than the executor's shared root batch
deadline. Earlier batch results are retained and never replayed. Calls without
`WorkBudget` keep the existing shared connection, batch lifetime and step limit.
No executor change or new wire field is required.

### Classifying failed mutations

For an error returned by Go `delegate.Client.Call`, `delegate.NotSent(err)` is
true only when the requested operation definitely did not execute. This includes
dial failures, local validation/size/deadline failures before send, queue or busy
backoff cancellation, and valid explicit pre-execution refusals such as
`ROOT_BUSY`, authentication, scope and policy denials. The refusal evidence is
generated from `spec/protocol/v1/errors.json` (`preAdmission`), independently of
the catalog's `retryable` flag. The classifier follows App error wrapping via
`errors.As`, and existing `*api.Error` codes remain available through that API.

For a non-nil error with `NotSent(err) == false`, the outcome may be unknown.
This includes partial writes, EOF/lost replies, non-timeout transport failures,
malformed/oversize replies, timeout or cancellation after send, unknown codes,
`WEBAPI_UNCERTAIN` and `WEBAPI_REPLY_TOO_LARGE`. The same
`EXECUTOR_UNAVAILABLE`, `EXECUTOR_TIMEOUT`, `EXECUTOR_PROTOCOL` or `canceled`
code alone cannot determine whether work ran. Nil and unrelated errors also
return false; apply the classifier only in the failed-call branch.

For writes, keep the original operation/job identity and read back the affected
state or receipt before deciding what to do. Do not blindly resend a mutation
whose outcome is unknown. A true result permits considering a new attempt, but
authorization/policy denials still require fixing the cause. The client itself
continues to retry only `ROOT_BUSY`. For a batch, classify each failed step:
an uncertain prior step does not mean a later unsent step ran, and earlier
successful steps must never be replayed.

## Outer DSM and proxy boundaries

Inspect the actual deployed route rather than assuming nginx's generic defaults.
A read-only inspection on 2026-10-29 of DSM 7.1.1 build 42962 Update 9 found:

- `/etc/nginx/nginx.conf`: the CGI location sends requests to the
  `synoscgi` upstream and explicitly sets `scgi_read_timeout 3600s`.
- `/usr/syno/share/nginx/cgi.mustache`: generated CGI routes set
  `scgi_read_timeout 3600s`; proxy routes in that template set
  `proxy_read_timeout 3600s`.
- The main HTTP settings include `client_body_timeout 60s` and
  `send_timeout 60s`. These govern stalled body input/output, not a total
  one-minute CGI handler execution budget.

The configuration evidence supports allowing a five-minute SDK request on that
route. It does not establish a universal DSM CGI process lifetime, a measured
five-minute response, or acceptance on another DSM version. Nginx read timeouts
are inactivity limits between reads, not total business-operation deadlines.
The inner `synoscgi` implementation, alternate Application Portal routes,
QuickConnect, ingress proxies and user reverse proxies can impose other limits.
Their effective end-to-end ceiling remains deployment-specific and must be
measured on the chosen route. Do not change DSM system configuration to make
long synchronous operations fit.

## Background admission and UI

1. Authenticate and validate a fixed operation, construct its owner from trusted
   identity, and use `NewRequestStore` / `SubmitSingleton` or `Submit`. Return
   the existing admission outcome and original job ID promptly.
2. Run trusted `Runner.Work` under the package account in its independent
   worker process or resident daemon. Use context-aware NAS calls and report
   phases with `Task.Report`; unknown totals stay unknown. Use the shared operation allowance, or an explicit trusted job allowance.
3. Authenticate each status/result/cancel request again. Poll by the original
   ID with bounded backoff; keep the last known state during read failures.
   A timed-out poll means status is unavailable, not that the job failed.
4. Show queued/running as "Still working", with phase/progress when known.
   An elapsed-time hint such as "Slow NAS" keeps the job running. Paused work
   needs resume; only terminal states determine completion.
5. After a lost admission response, reuse the same idempotency key and input,
   or read the known job. Never replay a dispatched mutation with a new key
   merely because a request timed out. Cancellation cannot undo remote effects.

See [background results](background-results.md) for runnable assembly fragments,
owner checks, structured results, persistence and ctl descriptors.

## Packaging and explicit overrides

Rebuild the App with matching Go/TS SDK versions. The packaging CLI already reads
the SDK timeout spec for generated request settings and the DSM window. Existing
manifest `backend.timeout_seconds` values remain explicit overrides, including
shorter ones; changing an SDK cannot change an installed binary or browser
bundle. Custom Go assembly must pass its deadline to `WithRequestDeadline` (and
`IngressTimeouts` when applicable) and give the browser the matching milliseconds.
Match a customized gateway header wait and every external proxy as well.

CLI skill/template guidance should teach the job admission/status/progress
pattern and the current SDK default. Keep separate ctl transport defaults and
approved executor policies explicit; this SDK change does not update those
repositories or publish an App.
