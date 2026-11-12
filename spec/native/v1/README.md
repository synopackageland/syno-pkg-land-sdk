# SynoPkgNative v1 — stable

`contract.json` is the single source for the page → shell messages and shell → page events, bridge capabilities and intake items. Run `node scripts/native-contract.mjs` to regenerate the TypeScript wire client and types; `--check` fails on drift. The TS build and gate check both generated drift and compatibility against the committed `baseline.json`. Generation also rejects incompatible changes before writing output. The baseline is the immutable stable floor; do not regenerate or edit it to make a breaking change pass.

The page API is `@synopackageland/sdk/mobile`. The DOM-free shell API is `@synopackageland/sdk/mobile/shell`; both ship in the same npm package, including declarations. The JSON contract ships at `spec/native/v1/contract.json`.

## Stability and compatibility

SynoPkgNative protocol 1 and the `mobile:` manifest declaration are **stable**.
Within this protocol, changes must be additive and optional: new optional
fields, new capabilities gated by `caps`, or new message/event types that a
receiver may ignore. Existing field names, meanings, types, required fields,
enum values and message/event variants remain supported. A new optional field
must have behaviour equivalent to its absence unless its capability was
negotiated. Adding an enum value must likewise preserve older receivers' ability
to ignore the feature. Passing the structural check does not replace review of
semantics, capability gating and browser degradation.

Any incompatible change requires a separate v2 contract and a bridge advertising
`protocol: 2`. The App must declare the appropriate `mobile.min_shell`; the shell
checks the generated registration's `minShell` before opening it and offers an
update or browser/desktop entry when unsupported. `min_shell` is a positive shell
feature level, not an SDK package version or a replacement for checking
`bridge.protocol` and `caps`. Do not silently interpret protocol 1 messages with
new meanings. The current manifest has no `protocol` declaration field; a future
protocol must define its negotiation alongside that contract.

The page calls `nativeBridge()` and uses native actions only when the protocol
and the individual capability are supported. Check `caps.download.base64`,
`caps.download.url`, the decoded `maxBase64` limit, `caps.openExternal` or
`caps.intake`, rather than inferring support from the shell's name/version.
`canSaveNativeURL()` exposes the URL-download check. An absent/false capability
uses the browser fallback described below; optional UI without a fallback stays
hidden or disabled. A malformed bridge or unsupported protocol is treated as a
browser host. Unknown capabilities and optional event fields do not invalidate
otherwise supported features; unknown message/event types are ignored.

The generated validators remain strict by default for outgoing data. Receivers
use, for example, `isNativeMessage(value, {allowUnknownFields: true})` before
handling known messages, ignoring unknown types. This ignores extension fields
while still validating known fields and refusing mixed download modes. Page
bridge detection and event listeners already use this receiver mode. Receiving
code must select the known fields it consumes; extension fields confer no
permissions and must not be forwarded into platform APIs.

## Stable mobile manifest

Declare `mobile:` in `dsm-package.yaml`. The CLI owns validation and packaging;
its [mobile schema](https://github.com/synopackageland/syno-pkg-land-cli/blob/main/schema/mobile.v1.schema.json)
and [integration guide](https://github.com/synopackageland/syno-pkg-land-cli/blob/main/skills/reference/mobile.md)
are the authoritative declaration and asset-generation rules. The same additive,
optional compatibility policy applies to this declaration. Unknown declaration
fields still fail CLI validation; use a CLI that supports any new optional field
before building. Already packaged declarations retain their meaning.

```yaml
mobile:
  entry: app
  start: /
  capabilities: [download, open-external]
  intake:
    route: /add
    accept:
      - {kind: url, schemes: [https]}
      - {kind: text}
  status: false
  min_shell: 1
```

`entry` is required: `app` uses the SDK entry page and `gateway` uses an existing
identity gateway. Optional `name` and `icon` default to the package's display name
and icon; `start` defaults to `/`. Routes must be local and start with a single
slash. `capabilities` defaults to an empty list and accepts `download`,
`open-external` and `camera`; these declarations request features, while injected
`caps` reports what this shell actually supports. Optional `intake` requires a
`route` and nonempty `accept` list: URL schemes, text, or file MIME types/extensions
with an optional `max_bytes` bounded by the intake limit. `status` defaults to
false and is reserved; protocol 1 does not invoke launcher status. `min_shell`
defaults to the shell feature level for protocol 1. Build emits `mobile.json`
with `schema: 1` and `minShell`; inspect checks its registration and asset digests.
DSM admission and App backend permissions apply independently of registration.

## Injection and trust

Inject `window.SynoPkgNative` before content load **only in a registered App's main frame at the NAS profile origin**. Its shape is `NativeBridge`: `{protocol: 1, shell: {name, version, platform}, caps, features?, post(message)}`. `platform` is `android` or `ios`. No bridge is injected for desktop-view/unregistered Apps, another origin, or any subframe. A child frame must not receive a forwarding shim. Recheck the frame URL on every navigation and incoming message; do not rely on a page-supplied origin. `canInjectNativeBridge` implements the origin/main-frame predicate; registration is checked separately by the shell.

The trust unit is the **NAS origin**, not a package path. All packages at that origin share trust; these checks do not create isolation between packages. The SDK's main-frame check is a defence in depth, not an authentication mechanism.

The bridge provides user-mediated actions only. It never returns cookies, tokens, local file paths or cross-origin credentialed requests. Log message types and sizes only, including for validation failures. Intake contents, download URLs and their query tokens must never be logged.

`features` is optional: page → shell requests this shell understands beyond the frozen v1 baseline (currently `close`). It sits beside `caps`, not in it, because SDK releases before the stable freeze validate `caps` without unknown fields and would drop the whole bridge. A page shows a control for a feature only when it is listed; a bridge without `features` offers none.

## Wire protocol

`post` takes a plain object validated as `NativeMessage` (not JSON text). The React Native transport may serialize it internally. Replies/events are `window.dispatchEvent(new CustomEvent('synopkgnative', {detail: event}))`, validated as `NativeEvent`. All messages/events have `v: 1`.

| Page → shell | Required fields and behaviour |
| --- | --- |
| `download` | `id`, `filename`, `mime`, exactly one of `base64` or `url`. `id` identifies subsequent transfer events. Base64 is standard padded base64; `maxBase64` is the decoded byte limit, up to 50 MiB. Save to app cache then show the share sheet |
| `download` with `url` | Absolute same-NAS-origin HTTP(S) URL only. Stream in-process using WebView cookies, then offer Share or Save to Downloads. Reject credentials in URLs. Revalidate redirects before following; never forward cookies to another origin or use DownloadManager |
| `openExternal` | HTTP(S) `url` only; require user activation, at most once per second, then open the system browser. Validate this in the shell even if the SDK validated it |
| `intake.take` | `id` is the opaque intake handle, also used to correlate its reply. Delete from the shell store before replying |
| `ready` | `fcp`: finite nonnegative milliseconds from page navigation to first contentful paint. Hide the splash |
| `close` | No fields. Leave the App for the shell's App list, as the shell's own Back does at the App's first page. Only offered when the bridge's `features` lists `close`; require user activation within the last second, ignore it from an App that is not on screen |

| Shell → page | Fields |
| --- | --- |
| `reply` success | `id`, `ok: true`, `items: NativeIntakeItem[]`. A missing/already-consumed handle returns an empty array |
| `reply` error | `id`, `ok: false`, `error: {code}`; no raw exception text |
| `launch` | Optional `link` (App-local route), `intake` (opaque handle); cold launch uses `?link=…&intake=…` instead |
| `transfer` | Download `id`, `state` (`queued`, `downloading`, `ready`, `shared`, `saved`, `canceled`, `failed`), optional `progress` (0…1) |

### Transfer acknowledgement and progress

For an accepted download, the shell emits `transfer` with the matching `id` and
`state: 'queued'` promptly, before cache preparation, network requests or other
potentially slow I/O. It then emits `downloading` and available progress, followed
by `ready` when bytes are available locally. User-mediated outcomes are `shared`,
`saved` or `canceled`; preparation/transfer failure emits `failed` with the same
`id`. A `queued` acknowledgement or `progress: 1` is not confirmation of a saved
file. A page subscribes with `onNativeTransfer` before dispatch and correlates
by `id`; acknowledgement can arrive before the action's promise resolves. Do not
leave a page waiting silently until the final share/save UI appears.

### Cold launch and splash

On cold launch, the shell passes validated launch parameters in the entry URL;
the page need not wait for a later `launch` event to navigate or consume intake.
After mounting, SDK entry boot sends `ready` when first contentful paint is
observable, using buffered paint entries if available. `fcp` is the measured,
finite nonnegative navigation-relative duration in milliseconds; it is not a
wall-clock timestamp or a fabricated value for an unavailable measurement.

The shell hides its splash on `ready`, and independently owns a bounded fallback
when paint observation or bridge delivery is unavailable. A load-end fallback
can reveal the page without a paint event; it must not synthesize `ready`, mark
FCP as measured, or hide a load/authentication error. Dismissal is idempotent,
including a late `ready` after fallback, and resets for each navigation. Page
mounting, identity checks and launch handling must not wait for splash dismissal.
The SDK does not own the shell's fallback timer or claim a universal cold-launch
latency. A missing paint sample stays missing in measurements.

### Read-once intake

Intake items are `{kind:'url', url}`, `{kind:'text', text}` or `{kind:'file', filename, mime, base64, size}`. A file is at most 8 MiB; the shell checks the decoded size against `size`, the App's declared maximum, and its accept rules before storing it. No local path is sent. Share payloads never go in URLs; only the handle does.

`createIntakeStore()` is a shell utility: `put(id, items)`, `take(id)` (read once), `clear()`. Use one store per NAS profile/session, issue unique random handles, and clear on sign-out/profile removal. Keep a warm WebView's queued intake until it requests the handle. A consumed handle cannot be reused in that store. The store holds items in memory, not logs or storage. The SDK consumes a handle before dispatch, so simultaneous calls or an uncertain response cannot reissue the read. The shell store enforces read-once across page reloads.

## Page integration

```ts
import {saveBlob, saveUrl, openExternal, routeExternalLinks, takeIntake,
  createNativeHost, nativeLaunchParams, onNativeLaunch} from '@synopackageland/sdk/mobile'

await saveBlob(new Blob(['csv'], {type: 'text/csv'}), 'report.csv')
await saveUrl('/webapi/entry.cgi?…', {filename: 'file.bin'})
// In a user gesture:
openExternal('https://example.org/')
const stopRouting = routeExternalLinks(document)
const items = await takeIntake('opaque-handle')
```

`saveBlob` and `saveUrl` sanitize filenames before either native dispatch or a
browser download. Separators (`/`, `\`) and C0/C1 control characters become
spaces. Whitespace and repeated dots collapse; spaces around dots and leading
or trailing spaces/dots are removed. Unicode names, including CJK and emoji,
survive. Names are capped at 255 UTF-8 bytes without splitting a code point;
the final extension is retained when it leaves room for a stem character. An
extension that cannot fit is truncated with the name. An empty result becomes
`download`. For example, `../報表\draft.csv` becomes `報表 draft.csv`.
Invalid MIME metadata is still rejected. The shell must independently validate
the wire filename as a basename, not accept paths; the strict wire contract is
unchanged. Page filename normalization is not authorization. The shell owns the final
platform filename: independently sanitize `/`, `\` and `?` to `_` in suggested
names, including names supplied by response metadata, before cache, share or
Downloads operations. Reapply basename/control-character and platform-length
checks after normalization, preserve safe Unicode and the extension where it
fits, and use `download` if empty. Never interpret a suggested name as a path.
A strict wire message containing a path is still rejected; these shell rules
also protect shell-owned response metadata and non-bridge download paths.
For example, a server-suggested `報表?draft.csv` becomes `報表_draft.csv`.
App source parsing can reject a filename before native dispatch; that does not
establish that the shell sanitizer ran.

Action results are `{mode:'native'|'browser', id?}`. They mean the action was dispatched, not that a transfer completed. Use `onNativeTransfer(listener)` for native progress, and dispose its subscription when unmounting. URL downloads may also be initiated by `createFileTransfers().download(path)`: with native URL support it builds the same-origin FileStation GET URL using the current SynoToken; browsers retain the isolated POST form.

With no valid bridge or missing capability, blobs use object-URL downloads, URLs use download anchors, external URLs use `window.open(..., '_blank', 'noopener')`, and intake returns `[]`. Oversize base64 or a foreign-origin download URL also uses the browser path. Object URLs stay alive until page departure to avoid revocation racing a slow download. Link routing leaves the browser's routing intact without the capability and preserves local links, download links and modifier clicks. Native dispatch failures fall back synchronously where possible; an uncertain intake read rejects without retrying. No request is aborted by a clock; `takeIntake(id, {signal})` permits caller cancellation of the wait and also cancels on page departure.

`Context.host` remains a `HostAdapter`. `createNativeHost().platform().host` is `native-shell` with a valid bridge, or `standalone-web` without one. Pass its snapshot overrides for DSM language/locale. Entry/context builders use `nativeLaunchParams({link, intake})` for cold launch and compose `onNativeLaunch(fn)` into `ctx.onLaunch(fn)`, retaining the returned disposer. Both produce existing `LaunchParams`: a validated `link` also becomes `route`, and `intake` stays an opaque handle. A browser subscribes to nothing. Existing `followLaunchRoute` therefore handles native routes as well.

The shell still handles OS-mediated file downloads, `_blank`, blob/download anchors and file chooser actions. These are separate from bridge injection. Real-device download/cookie/activation acceptance belongs to the shell acceptance suite; SDK tests establish wire shapes, fallbacks and handoff only.
