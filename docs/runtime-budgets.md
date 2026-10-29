# Runtime budgets

Work budgets do not change session, token, grant, login-rate or WebSocket
revalidation bounds. Configure work through existing manifest settings and rebuild
with a matching CLI/SDK. Lifecycle budgets are policy limits, not evidence that
DSM or Container Manager permits that much time.

| Work | Author setting | Behavior |
| --- | --- | --- |
| Compose start | `compose.startup_timeout_seconds` (900, 2–900) | One monotonic deadline includes lock, status, spawn, readiness, stability and handoff. The default lets a loaded HDD NAS with 2 GB RAM start an App; declare a shorter value only with device evidence. The 900-second cap remains until native outer limits are verified. |
| Native lifecycle | `daemon.timeouts` | Start/stop/uninstall accept 2–3600 seconds; status accepts 1–3600. Defaults remain 30/30/60/5. A declaration containing only `timeouts` starts no daemon and works with Compose and background workers. |
| Request/operation | `backend.timeout_seconds` (300, 1–3600) | The SDK request and unconfigured operation allowance default to 300 seconds. The matching CLI reads the request default from the SDK's `spec/protocol/v1/timeouts.json` and passes the manifest budget to the backend, browser, supervisor, gateway token adapter and approved executor policy. Explicit values remain authoritative. Each nested operation is clipped to remaining caller time and leaves reply reserve. Binary/Compose Apps without a CGI request setting can enlarge the shared operation allowance through `daemon.timeouts.status`. |
| Bridge cold start | `backend.timeout_seconds` | Interpreter detection and hello share half the request allowance, leaving time for the call and cancellation. An explicit Go `Config.StartTimeout` overrides the boot share. |
| App uninstall hook | `uninstall.purge.timeout` (60, 2–3600) | Includes TERM/reap reserve, uses elapsed monotonic time on Linux, and retains a pending marker when termination cannot be confirmed. |
| CLI transport/wait | `ctl --timeout` (1–3600 seconds) | Applies to discovery, JSON calls, polling and transfer inactivity. Default transport allowance is 62 seconds. An operation shares its remaining allowance across calls; jobs retain their descriptor wait default when no override is supplied. |
| Jobs | `background.Runner.Timeout` (30 minutes by default) and existing root program limits | Package-account job execution is independent of HTTP. Root program policy limits remain separately approved; shutdown grace is clipped to the lifecycle stop allowance. |

The browser waits 302 seconds by default; the identity gateway waits 310 seconds
for upstream response headers unless configured otherwise. Background downloads
allow 30 minutes for the transfer and 300 seconds for headers. Journal lock
contention remains bounded to five seconds. Executor batches retain their
60-second protocol cap. See [request deadlines](request-deadlines.md) for the
audit, outer proxy limits and the background job pattern for slow NAS work.

The separate CLI `ctl` transport can still impose its own 62-second default;
use an explicit `--timeout` for longer synchronous calls. An SDK update does not
change an already built App, an explicit manifest value or a separate ctl binary.

Each readiness probe may take up to 30 seconds (clipped to the operation budget),
independently of the DSM status command budget. After startup, a failed or delayed
availability probe closes new ingress without stopping the running App: the gateway
shows a localized, self-refreshing "starting" page (HTTP 503, `Retry-After`) and
API clients receive `app_starting`. DSM status keeps reporting the package as
running. Successful recovery reopens admission. Availability failure that lasts
longer than the startup budget (at least the operation budget) stops the supervised
child and records the phase and duration in `daemon/daemon.log`. Policy, network ownership, installed version and lifecycle
generation failures remain immediately fatal. The window never extends on retries.
Public and passthrough upstream routes use the same readiness gate.

An offline request without an observed executor transition remains pending.
A stop shares its deadline across command lock, supervisor dispatch, child TERM,
reap and final inventory retirement. Executor job drain starts its total stop
allowance before the root stop step. Purge has a separate bounded cleanup allowance;
unconfirmed AppPriv/portal cleanup retains protected state and the pending request.
Portal rollback has its own small bounded reserve and retains cleanup-required on
failure. No HTTP caller can enlarge a pinned privileged work budget.

Blocking filesystem I/O, process reap in an uninterruptible kernel state and DSM's
outer lifecycle limits remain outside a cooperative deadline guarantee. The package
helper acknowledges accepted work separately from confirmed cleanup; no NAS
acceptance is implied by configuration validation.

### Remaining work deadline

A single-request frame may include `deadline_ms` (integer 1–3600000). It is
remaining work time measured immediately before dispatch, excluding the client's
response reserve. The executor uses the lesser of this and the approved policy's
operation budget; `root.run` retains its existing program and reply caps. Omitted
or zero preserves the legacy operation bound. Negative or excessive values are
refused. A deadline grants no authority and never changes any credential lifetime.
A timeout after dispatch leaves a mutation's completion uncertain; clients must
read back state and must not replay it automatically. Batch caps are unchanged.

An optional policy `timeouts` object pins `start`, `stop`, `purge`, and `operation`
seconds (`start`/`stop`/`purge` 2–3600; `operation` 1–3600). Changes require approval of the new policy digest. The
package helper reads its own copy only to bound its wait. Its JSON result separates
`accepted`, `cleanup-complete`, and `cleanup-pending`; a successful helper exit
acknowledges the retained request, and does not prove cleanup completed.
## Readiness status during a probe

The supervisor queues at most 64 authenticated status requests while its current
readiness probe runs, bounded by the existing status timeout. It returns that
probe's result instead of briefly answering `unknown` every time the one-second
poll begins. It does not reuse an old healthy result. Real failed probes still
deny admission; timeout, shutdown or queue saturation fails closed. Timers close
abandoned waiters and shutdown closes pending sockets without delaying stop.

The regression test executes the compiled controller as a nonroot fixture with
a deliberately held HTTP probe: no reply while pending, `ok` after HTTP 204,
and `unknown` after HTTP 503. The original implementation answered `unknown`
immediately during the held healthy probe.
