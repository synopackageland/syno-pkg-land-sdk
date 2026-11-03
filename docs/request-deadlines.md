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
