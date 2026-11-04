# HTTPS requirements and stale DSM sessions

Many Apps work over HTTP. Do not force HTTPS for ordinary CRUD, settings or
file interfaces. Require it only when the App needs secure context features
such as `getUserMedia`, clipboard access, Service Worker or WebCrypto `subtle`,
browser Extension or external device pairing, or an explicit secure Origin
policy. Browser feature availability still depends on its own permissions.
See the CLI's [HTTPS skill](https://github.com/synopackageland/syno-pkg-land-cli/blob/main/skills/SKILL.md#https-與舊-dsm-token)
for the App workflow and test guidance.

## Optional HTTPS guard and presentation

Go `api.RequireSecureOrigin(request)` returns `*api.Error` with code
`https_required` unless the browser's Origin (or Referer when Origin is absent)
has an HTTPS scheme and a valid host without credentials. Missing, opaque and
malformed sources are refused. It reads the browser source, not the backend
socket's TLS or forwarded scheme. Call it only in Apps that require HTTPS,
from their existing request middleware, and propagate it in the usual SDK
error envelope before business work. Keep session, `api.CheckOrigin` and route
permissions: this helper checks a scheme, not identity or same-origin access.
It is not installed by default and does not change ordinary HTTP Apps.

Return `https_required`, never a generic `NOT_AUTHENTICATED` or
`session_required` for this refusal. The frontend should show a dedicated
HTTPS page with `identityCopy(locale).httpsTitle` and
`identityCopy(locale).errors.https_required`, rather than asking the user to
sign in repeatedly. UI labels omit terminal periods. The existing copy is
shared by all SDK locales; an App may supply more specific localized guidance.

Render the instructions first. Only `ctx.user?.isAdmin === true` may call
`readDsmPorts(ctx)` and use the `dms_https` single TCP port for a link. PortInfo
is administrator-only: invoking it as an ordinary user may refuse the call or
close the DSM window. SDK `readDsmHttpsURL` handles the administrator check:

```ts
import {identityCopy, readDsmHttpsURL} from '@synopackageland/sdk'

const copy = identityCopy(ctx.locale)
// Render copy.httpsTitle and copy.errors.https_required immediately.
// For unknown/ordinary users this returns undefined without calling PortInfo.
const dsmURL = await readDsmHttpsURL(ctx, location.href, signal)
// Offer the link only if present; explain reopening the App after entering DSM.
// To reopen the App when the current URL has DSM launch parameters:
const appURL = await readDsmHttpsURL(ctx, location.href, {signal, keepLaunchApp: true})
```

The helper keeps the current hostname, selects the declared HTTPS port and
opens `/webman/`, removing page paths, query strings and fragments. It does
not redirect automatically or forward session tokens. Pass `{keepLaunchApp: true}`
(optionally with `signal`) to retain only `launchApp` and its associated
`launchParam` from the current URL. Other query parameters and the fragment
are still removed. This does not invent a route for URLs without `launchApp`;
the administrator-only discovery rule stays the same. Existing third-argument
`AbortSignal` callers continue to work. Missing, ambiguous,
refused or timed-out discovery leaves instructions only; it never guesses
a default port. A declaration does not prove that the port is reachable or
that a reverse proxy exposes it. The App owns that deployment-specific guidance.
A new universal Vue page is unnecessary: use the existing localized copy in
the App's shell, preserving its own routing and login layout.

## Opt-in read RPC recovery

A DSM page can retain an old `X-SYNO-TOKEN` after another tab signs in again.
`createDsmSession({read, write, fetch})` exposes `refresh(rejectedToken)` to
recover through the same-origin DSM login endpoint even while `read()` still
reports the rejected token. `token()` then prefers the recovered carrier until
the desktop supplies a different one. Concurrent recovery is shared; a newer
desktop token wins over an older response. Nothing enters storage or URLs.
Refresh failure or a reply with the same rejected token returns `undefined`,
so the client does not replay. Both leave the existing carrier available to
`token()` and `ready()`, including for later writes and ingress streams.
The carrier changes only after successful recovery or when `read()` changes;
refresh is not a sign-in flow.

`createClient` can refresh and retry one single-call JSON read on a decoded
`session_required` refusal:

```ts
import {createClient, createDsmSession} from '@synopackageland/sdk'
import {sessionRetryOperations} from './generated/session-retry'

const session = createDsmSession({
 read: () => ctx.files?.token?.(),
 fetch: (_relative, init) => fetch('/webman/login.cgi?enable_syno_token=yes', init),
})
const client = createClient({
 endpoint: '/webman/3rdparty/Example/cgi/api.cgi',
 requestTimeout: ctx.requestTimeout,
 sessionRetry: {
  session,
  operations: sessionRetryOperations,
 },
})
await client.call('settings.get', {}, {signal})
```

`sessionRetry.session` supplies this client's token and readiness callbacks.
Run `synopkgland ctl generate` to emit `generated/session-retry.ts` from the
operation registry and exported route declarations. Commit that artifact;
`ctl generate --check` and normal check/build detect drift. Its exported
`sessionRetryOperations` is a literal effect/kind map that can be passed directly
to the SDK. The map is trusted App assembly metadata, snapshotted at client
creation. Declare optional registry `kind: rpc | ai | stream` when needed;
mark model execution `kind: ai`, even if it returns JSON.

Without `kind`, the generator uses actual JSON route declarations for `rpc`,
and transfer/stream/download/export routes or NDJSON/upload flows for `stream`.
Known non-RPC declarations take precedence over `kind: rpc`. An unclassified
route yields `kind: unknown` and cannot retry. The registry's `ai: read` class
describes AI tool exposure, not model execution; it never establishes replay
safety. Neither method-name prefixes nor caller-provided params establish effects.

Only `effect: read` plus `kind: rpc` may retry. Writes, destructive calls, AI,
streams, unknown kinds/methods and all batches never retry. Other refusal codes,
HTTP/network/JSON failures and timeouts do not trigger recovery. A second
refusal propagates without another refresh. Failed recovery retains the
original refusal. The original serialized params, cancellation signal and
total request allowance cover the first call, refresh and single replay.
Canceling one caller stops its replay; a shared refresh may still finish for
other callers under its own bounded allowance.

Recovery is disabled by default. `createClient` does not have the App's
authoritative operation effects or AI/stream classification, and generic
`ctx.call` is also used for writes. Enabling retries for all methods would be
unsafe. Once an App supplies this trusted policy, eligible reads recover
automatically. Existing CLI-generated `ctx.call` and `ctx.batch` are not
rewrapped by upgrading the SDK; bind an explicitly configured client where
needed. Backend authentication and permissions remain authoritative on replay.

## Validate the App

Open through both `http://<host>:5000` and `https://<host>:5001`, substituting
actual DSM ports. Verify ordinary HTTP-capable Apps still open. An HTTPS-only
App must display `https_required` with usable instructions on HTTP and work
on HTTPS. Test administrator and ordinary users separately, including that
ordinary users never invoke PortInfo. Test unavailable port discovery too.

Keep an old page open, sign in again in another tab, then exercise a declared
read. Verify at most one refresh and replay, concurrent reads, failed recovery,
cancellation and a second refusal. Confirm writes, AI, streams and batches
are dispatched once. Unit fixtures establish these transport boundaries;
they do not certify a live DSM session, secure context feature or proxy setup.
