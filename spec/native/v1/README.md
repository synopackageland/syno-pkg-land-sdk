# SynoPkgNative v1

`contract.json` is the single source for the page → shell messages and shell → page events, bridge capabilities and intake items. Run `node scripts/native-contract.mjs` to regenerate the TypeScript wire client and types; `--check` fails on drift. The TS build and gate check drift.

The page API is `@synopackageland/sdk/mobile`. The DOM-free shell API is `@synopackageland/sdk/mobile/shell`; both ship in the same npm package, including declarations. The JSON contract ships at `spec/native/v1/contract.json`.

## Injection and trust

Inject `window.SynoPkgNative` before content load **only in a registered App's main frame at the NAS profile origin**. Its shape is `NativeBridge`: `{protocol: 1, shell: {name, version, platform}, caps, post(message)}`. `platform` is `android` or `ios`. No bridge is injected for desktop-view/unregistered Apps, another origin, or any subframe. A child frame must not receive a forwarding shim. Recheck the frame URL on every navigation and incoming message; do not rely on a page-supplied origin. `canInjectNativeBridge` implements the origin/main-frame predicate; registration is checked separately by the shell.

The trust unit is the **NAS origin**, not a package path. All packages at that origin share trust; these checks do not create isolation between packages. The SDK's main-frame check is a defence in depth, not an authentication mechanism.

The bridge provides user-mediated actions only. It never returns cookies, tokens, local file paths or cross-origin credentialed requests. Log message types and sizes only, including for validation failures. Intake contents, download URLs and their query tokens must never be logged.

## Wire protocol

`post` takes a plain object validated as `NativeMessage` (not JSON text). The React Native transport may serialize it internally. Replies/events are `window.dispatchEvent(new CustomEvent('synopkgnative', {detail: event}))`, validated as `NativeEvent`. All messages/events have `v: 1`.

| Page → shell | Required fields and behaviour |
| --- | --- |
| `download` | `id`, `filename`, `mime`, exactly one of `base64` or `url`. `id` identifies subsequent transfer events. Base64 is standard padded base64; `maxBase64` is the decoded byte limit, up to 50 MiB. Save to app cache then show the share sheet |
| `download` with `url` | Absolute same-NAS-origin HTTP(S) URL only. Stream in-process using WebView cookies, then offer Share or Save to Downloads. Reject credentials in URLs. Revalidate redirects before following; never forward cookies to another origin or use DownloadManager |
| `openExternal` | HTTP(S) `url` only; require user activation, at most once per second, then open the system browser. Validate this in the shell even if the SDK validated it |
| `intake.take` | `id` is the opaque intake handle, also used to correlate its reply. Delete from the shell store before replying |
| `ready` | `fcp`: finite nonnegative milliseconds from page navigation to first contentful paint. Hide the splash |

| Shell → page | Fields |
| --- | --- |
| `reply` success | `id`, `ok: true`, `items: NativeIntakeItem[]`. A missing/already-consumed handle returns an empty array |
| `reply` error | `id`, `ok: false`, `error: {code}`; no raw exception text |
| `launch` | Optional `link` (App-local route), `intake` (opaque handle); cold launch uses `?link=…&intake=…` instead |
| `transfer` | Download `id`, `state` (`queued`, `downloading`, `ready`, `shared`, `saved`, `canceled`, `failed`), optional `progress` (0…1) |

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
unchanged. Filename sanitization is a page API behaviour, not authorization.

Action results are `{mode:'native'|'browser', id?}`. They mean the action was dispatched, not that a transfer completed. Use `onNativeTransfer(listener)` for native progress, and dispose its subscription when unmounting. URL downloads may also be initiated by `createFileTransfers().download(path)`: with native URL support it builds the same-origin FileStation GET URL using the current SynoToken; browsers retain the isolated POST form.

With no valid bridge or missing capability, blobs use object-URL downloads, URLs use download anchors, external URLs use `window.open(..., '_blank', 'noopener')`, and intake returns `[]`. Oversize base64 or a foreign-origin download URL also uses the browser path. Object URLs stay alive until page departure to avoid revocation racing a slow download. Link routing leaves the browser's routing intact without the capability and preserves local links, download links and modifier clicks. Native dispatch failures fall back synchronously where possible; an uncertain intake read rejects without retrying. No request is aborted by a clock; `takeIntake(id, {signal})` permits caller cancellation of the wait and also cancels on page departure.

`Context.host` remains a `HostAdapter`. `createNativeHost().platform().host` is `native-shell` with a valid bridge, or `standalone-web` without one. Pass its snapshot overrides for DSM language/locale. Entry/context builders use `nativeLaunchParams({link, intake})` for cold launch and compose `onNativeLaunch(fn)` into `ctx.onLaunch(fn)`, retaining the returned disposer. Both produce existing `LaunchParams`: a validated `link` also becomes `route`, and `intake` stays an opaque handle. A browser subscribes to nothing. Existing `followLaunchRoute` therefore handles native routes as well.

The shell still handles OS-mediated file downloads, `_blank`, blob/download anchors and file chooser actions. These are separate from bridge injection. Real-device download/cookie/activation acceptance belongs to the shell acceptance suite; SDK tests establish wire shapes, fallbacks and handoff only.
