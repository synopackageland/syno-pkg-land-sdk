# Safe self-package restart

Use Go `selfrestart.New` / `Service.Routes` with the protected native host's
`createSelfRestart` from `@synopackageland/sdk/native`. The App supplies its
existing protected confirmation consumer; the SDK owns admission, the fixed
native dispatch flow, durable references and outcome validation. An AI receives
only draft, preview and readback operations. It never receives an approval
handle, dispatch permit or the confirm/record/progress routes.

The App runs as its package account. Its worker cannot sequence its own stop
and later start. The administrator's native DSM transport instead sends one
`SYNO.Core.Package.bulk` v1 request containing exactly the installed package's
`stop` then `start`, with `reorder: false`. DSM executes it outside the target
package's worker lifetime. This is not a `Package.Control.restart` API. There
is no root helper, scheduler task, arbitrary command transport or saved session.

## Install and assemble

Package an immutable JSON declaration, for example:

```json
{"schema":1,"package":"Example","version":"1.0.0-0001","enabled":true}
```

Hash its exact installed bytes (including any newline) into the trusted App
build. Policy path, digest, state directory and approval consumer are assembly
values, never request parameters. The digest is checked again on every call.
The state directory must be private, owned by the nonroot package account.
Serve the evidence/readback routes from the package's stable CGI, including
while its worker is stopped. Putting them only behind the resident daemon's
ingress cannot record a failed start until an administrator restores the daemon.

```go
restart, err := selfrestart.New(selfrestart.Config{
    PolicyPath: installedRestartPolicy,
    PolicySHA256: pinnedRestartPolicySHA256,
    StateDir: filepath.Join(packageData, "self-restart"),
    Approval: protectedConfirmations,
    ActiveWork: activeWorkSample,
})
if err != nil { return err }
routes = append(routes, restart.Routes()...)
```

`ActiveWork` returns sanitized `{kind, reference}` entries from the App's real
active operations. Fetch at most eleven entries; the preview exposes at most
ten and marks truncation. Include AI turns, transfers and worker jobs that the
App owns. An empty result means there are currently no sampled active jobs.
Never include prompts, arguments, paths containing sensitive data or credentials.

The preview includes `action: "restart"`, its digest and interruption categories
`all-apps`, `ai-turns`, `transfers`, `workers`. The protected dialog must render
these consequences and the sample before offering confirmation.

`Approval.Consume(ctx, caller, reference, previewDigest, handle)` must atomically
consume an existing protected one-use confirmation bound to the freshly verified
administrator, reference and exact preview digest. It must reject AI-issued,
expired, reused or mismatched handles. It must not recursively call the restart
service while consuming. Do not implement this interface as `return nil`, a
client boolean, an AI confirmation string or a check of the administrator's name.
The SDK independently requires an authenticated administrator on every route;
role changes and a changed policy refuse further admission/progress.

Declare the routes in the App's operation registry and run the existing registry
generator. Keep `package.restart.confirm`, `.record`, `.progress`,
`.reconciliation_preview` and `.acknowledge` hidden from AI and ctl. Offer
`.draft` as a proposal, `.preview` as read-only and `.get` as read-only.
All routes require administrators inside their handlers, even if App metadata
declares a weaker permission. During metadata-only route export a nil
`*selfrestart.Service` can return `Routes()`; its handlers always refuse.

## Native host

```ts
import {createSelfRestart, createSelfRestartTransport} from '@synopackageland/sdk/native'

const restart = createSelfRestart({
  package: installedPackagePin,
  version: installedVersionPin,
  call: protectedBackendCall,
  send: createSelfRestartTransport(request =>
    nativeWindow.sendWebAPI({...request, scope: nativeWindow})),
  isAdmin: currentNativeAdministrator,
  confirm: showExistingProtectedConfirmation,
})
const draft = await restart.draft()
const preview = await restart.preview(draft.reference)
// A person acts in the protected host; no model calls this method.
const outcome = await restart.confirm(draft.reference)
// Save/share the reference, never the private dispatch permit.
const later = await restart.get(draft.reference)
```

`send` is DSM's native same-origin `sendWebAPI` adapter: it returns the unwrapped
native `data`. Preserve complete bulk failure evidence by rejecting native
failures with `{code, nativeResult: response.errors}` when DSM supplies those
results. Transport errors and adapters that discard the native failure details
remain unknown. Do not replay browser cookies through
a backend HTTP connection or use a generic delegated WebAPI profile. The native
host owns `confirm` and its permit in a closure, outside model-visible App data.
The confirmation UI must distinguish `action: "restart"` from the separate
`"acknowledge-unknown"` action described below.

`Service.Draft`, `Preview`, `Confirm`, `Get`, `Progress`, `Record`,
`ReconciliationPreview` and `AcknowledgeUnknown` are also available to Go
handlers. `Routes()` provides their fixed JSON routes. Neither API accepts a
caller-selected package, executable, arguments or account.

## Durable admission and outcomes

Draft persists a random 256-bit reference. Confirm serializes independent CGI
instances under a private filesystem lock, consumes the bound protected approval
and durably spends exactly one dispatch permit before returning it. A duplicate
confirmation returns `dispatch: false` and no permit. At most one unresolved
restart occupies the package, across administrators and CGI processes.

The native host durably records `stopping` before sending the single batch and
`starting` after it returns. It never retries bulk, stop or start. A lost confirm
or progress response can leave an operation unexecuted; the original reference
is still spent. Reconcile that reference instead of creating another mutation.

| State | Meaning |
| --- | --- |
| `draft` | Persisted proposal; no lifecycle action admitted |
| `accepted` | One permit durably issued; no claim that DSM accepted the batch |
| `stopping` | Native host recorded its dispatch boundary |
| `starting` | Native batch replied; final running readback is pending |
| `confirmed-running` | Complete matching stop/stopped and start/started native results, both successful, followed by matching running/version readback |
| `failed` | Matching complete native stop or start result reports failure |
| `refused` | Protected confirmation was refused; no dispatch permit was issued |
| `unknown` | Native completion evidence is missing, incomplete or mismatched |

Permission/approval/policy refusal returns `self_restart_refused` or
`admin_required` before a permit is issued. A protected confirmation refusal is
also retained as `refused` under the original reference. Native admission errors are unknown
unless completion evidence establishes a failure; a rejected transport alone
does not prove that nothing happened. Pending `.get` reads return `unknown` with
the last durable phase, because a fresh reader cannot know whether the original
native connection still runs. A new daemon or DSM `running` alone never confirms
success. Worker/job timeouts do not decide this lifecycle outcome.

`Record` accepts a bounded observation only with the private original permit and
fresh owner session, verifies the complete package/version/action tuple and
sequence, and stores a normalized outcome. This is evidence from the trusted
native host, not a cryptographically signed DSM attestation. Do not expose the
observer's private routes or permit to an assistant. No raw DSM reply, cookie,
password, SynoToken, approval handle or permit is retained in the journal.

After the host records a terminal result, the original browser/AI may disappear;
another authenticated connection reads it by reference. Losing the native reply
before recording leaves `unknown` even when DSM actually restarted the package.
DSM bulk supplies no caller-bound idempotency key or durable receipt lookup.
The SDK deliberately does not infer success from unrelated lifecycle history.

## Manual reconciliation

If DSM reports the package stopped after a native start failure, an administrator
starts the same package in Package Center. There is no worker self-healing or
automatic repeat of a possibly accepted mutation. If the package is running but
the original native result is lost, inspect Package Center and keep the original
outcome unknown.

After manual investigation, `restart.acknowledge(reference)` presents a separate
protected confirmation. `AcknowledgeUnknown` leaves the original result unknown,
marks it operator-acknowledged, releases the singleton and never issues a permit
or calls DSM. A deliberate later restart needs a new draft and confirmation;
the original reference is never reusable. The bounded journal retains at most
100 records; only finished or operator-acknowledged records can be evicted to
admit another draft. Unresolved operations are never silently discarded.

## Device boundary

The fixed native batch has been exercised on DSM 7.4.1 with an isolated native
package, including loss of the request connection during stop. Local tests cover
admission concurrency, cancellation of lock waits, policy/role changes and
conservative receipt validation. This is not blanket certification of all DSM
versions or package dependency graphs. DSM remains the owner of its package
locks and dependency semantics; concurrent Package Center administration is not
an atomic SDK transaction. There is no fallback on an unsupported native method.
NAS reboot during a batch can destroy DSM's in-flight executor; retain unknown,
inspect Package Center and never automatically replay the original request.
