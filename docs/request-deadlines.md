# Slow NAS work and request deadlines

The default SDK request allowance is five minutes. The Go API, bridge runtime,
daemon JSON ingress and headless TS client use the same request contract in
`spec/protocol/v1/timeouts.json`: 300 seconds of work, with the browser waiting
two seconds longer. Preserve explicit App settings and earlier parent deadlines.
This is a finite cooperative work budget, not an end-to-end availability promise.

Long or unpredictable NAS operations should use the existing package-private
background spool and worker. A larger request allowance is useful for bounded
synchronous operations; it does not make a CGI request durable.

## Request path audit

| Boundary | Decision and current behavior |
| --- | --- |
| `api.DefaultRequestTimeout`, `WithRequestDeadline` | The old one-minute default truncated ordinary slow NAS work. Default to 300 seconds. An existing deadline is retained by Server; wrapping with a shorter deadline still wins. Handlers and dependencies must honor context cancellation. |
| `api.ServeIngress` | JSON/transfer requests inherit the same default. Raw `HandleHTTP` routes retain their separate one-hour stream allowance and configured nginx timeout. Read-header and shutdown limits do not bound completed-header business work. |
| TS `DEFAULT_REQUEST_TIMEOUT`, `clientTimeout`, `createClient` and NAS provider | Default to 300000 ms plus a 2000 ms response reserve. Explicit `requestTimeout` still wins. A slow call is not canceled at one minute. Cancellation still aborts immediately; no mutation is replayed automatically. |
| Direct browser File Station reads | Default to the same 302-second client allowance; an explicit `DsmFileOptions.timeout` wins. Uploads retain their separate 20-minute default. These are DSM requests, not a package-account proxy. |
| Legacy Go `filestation.Service` | Keep the existing 20-second verification/import/browse/check bounds in this change. The default Go client refuses file proxy operations; the supported browser path has the larger allowance above. Custom DSMClient adapters that perform slow file import/browse remain a tolerance concern: use independent jobs or separately review their operation budgets. They do not inherit a five-minute file-operation allowance merely from the API default. |
| `internal/runtimebudget.Operation` | The unconfigured operation fallback is 300 seconds, including bridge interpreter discovery and supervisor recovery windows. Configured operation budgets still win; nested calls retain reply/cleanup reserve. |
| Bridge cold start | Without an explicit `Config.StartTimeout`, hello shares half the request budget, including when the manifest omits a timeout. This avoids an independent hardcoded one-minute fallback. |
| `background.JobTimeout` | One minute also truncated jobs after HTTP was removed. Default to 30 minutes. Trusted assembly can choose `Runner.Timeout`; cancellation, pause, stop, crash recovery and spool quotas retain their semantics. |
| `background.RequestLockTimeout` | Keep five seconds. This bounds contention on a local journal lock, not execution of NAS work. Return busy and retry admission with the same key; lengthening this wait would hold browser requests behind a stalled journal. |
| `background.ScheduleWindow` | Keep 60 seconds. This is the scheduler tick window, not a job execution cap. Missed schedules are not replayed. |
| `delegate.DownloadTimeout` and download HTTP client | Default to 30 minutes for the background transfer, with 300 seconds for response headers. The old 60-second total and ten-second header wait were independent slow-origin failures. Size limits, five-second connection establishment, no redirects, final write authorization, revocation and existing grant expiry remain enforced. Queue wait does not renew a grant. |
| Delegate batch / executor `maxBatchDuration` | Keep the 60-second cap and 32-operation limit on a single privileged connection. A larger browser deadline does not enlarge this protocol bound. Split bounded batches in trusted worker code or use approved root jobs for long privileged work. |
| Executor ordinary calls, `root.run`, commands and lifecycle | Keep existing per-operation/program caps and pinned `timeouts` policy. Remaining request time can only reduce an approved bound. Changing privileged policy requires approval; long root work uses `root.start` / `root.jobs.*`. No generic HTTP change extends authority. |
| `idgateway.ResponseHeaderTimeout` | Default to 310 seconds, leaving a ten-second reserve past the standard App deadline. An explicit gateway setting wins and must exceed a customized upstream deadline. Upstream bodies are not covered by this header timer. |
| Gateway operation / token adapter | Unconfigured operation fallback is 300 seconds. Explicit operation settings and the standalone `ExecTokens` ten-second adapter fallback remain distinct. Token/session/assertion lifetimes, directory authentication bounds and revalidation are not NAS job budgets. |
| Connection, authentication, cleanup and readiness probes | Keep bounded establishment/lookup/cleanup limits. They protect admission or cleanup, not long business work. Supervisor availability has its existing tolerant starting/recovery window; the operation fallback now allows five minutes. Real authority failures remain immediate refusals. |
| Separate CLI `ctl` and Compose planning command | Their own transport/planning budgets are outside this request default. Use an explicit ctl timeout for long synchronous calls, or nonblocking job submission and status reads. The SDK Compose plan command retains its 60-second bounded planning call. |

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
   phases with `Task.Report`; unknown totals stay unknown. Set a finite
   execution allowance appropriate to the fixed operation.
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
