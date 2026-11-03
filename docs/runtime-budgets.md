# Runtime budgets

Work budgets do not change session, token, grant, login-rate or WebSocket
revalidation bounds. Configure work through existing manifest settings and rebuild
with a matching CLI/SDK. Lifecycle budgets are policy limits, not evidence that
DSM or Container Manager permits that much time.

| Work | Setting / default | Behavior |
| --- | --- | --- |
| Synchronous work, jobs, transfer, cleanup | `runtimebudget.Operation`: 300 seconds | One shared default; earlier cancellation wins. Explicit trusted/approved settings remain effective. |
| Bridge cold start | `backend.timeout_seconds`, `Config.StartTimeout` | Hello and interpreter discovery share the work allowance; no fractional cold-start cutoff. |
| Browser | `DEFAULT_REQUEST_TIMEOUT` + 2000 ms | Same request allowance plus reply reserve, including File Station uploads and session recovery. |
| Executor | approved `timeouts` and root program settings | Unconfigured work uses 300 seconds. Root run supports explicitly approved 1–3600 seconds; no new authority is granted by a deadline. |
| Compose readiness | caller wait, then continuing supervision | Gateway stays alive and shows starting until fresh readiness succeeds; availability never exhausts a failure window. |
| Separate CLI | `ctl --timeout`, generated manifests | An older CLI can still inject shorter settings; update that repository separately and rebuild the App. |

No module may create an independent short operation timeout. Retained intervals,
response reserves, framing, authority lifetimes and post-cancellation reap timing
are individually recorded with reasons in `scripts/timeout-allowlist.json`.
The gate checks new literal clocks and the import-free generated budget copies.
See [request deadlines](request-deadlines.md).

An offline request without an observed executor transition remains pending.
A stop shares its deadline across command lock, supervisor dispatch, child TERM,
reap and final inventory retirement. Executor job drain starts its total stop
allowance before the root stop step. Purge has a separate bounded cleanup allowance;
unconfirmed AppPriv/portal cleanup retains protected state and the pending request.
Portal rollback uses the shared operation allowance and retains cleanup-required on
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
read back state and must not replay it automatically. Batches share the operation default and retain their 32-operation cap.

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
