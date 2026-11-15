# Entry pages

`@synopackageland/sdk/entry` supplies a cookie-backed full-page boot without
loading DSM's desktop scripts. The CLI bundles it into the package; no runtime
registry or CDN is needed.

```ts
import {bootEntryPage} from '@synopackageland/sdk/entry'

await bootEntryPage({
 packageId: 'ExampleApp',
 build: 'the-cli-build-hash',
 root: document.querySelector<HTMLElement>('.dsmapp-root')!,
 displayName: 'Example App',
 appBundle: './app.module.js?v=the-cli-build-hash',
 mount: async (root, ctx) => {
  const app = await import('./app.module.js?v=the-cli-build-hash')
  return app.mount(root, ctx)
 },
 // Omit portal when this App has no Application Portal alias.
 portal: {defaultAlias: 'example-app'},
})
```

`appBundle` declares the same module URL imported by `mount`, including its
build query. The SDK resolves it against the original entry URL and starts a
`modulepreload` before waiting for the token or identity. Only HTTP(S) URLs at
the entry origin without embedded credentials are allowed. Preloading fetches
the module and may parse its dependencies; it does not execute them. `mount`
still runs only after successful token and identity verification, and imports
the already requested module. Failed identity never invokes `mount`.

Pass this option from the App entry or generator to enable early loading. It
is optional for existing callers; the SDK cannot infer a module URL hidden in
a mount callback. A missing or failed preload keeps the ordinary import path.

DSM serves package files without `Cache-Control` and with a 1970
`Last-Modified`, so browsers keep them for years and open them without asking
DSM, even after an upgrade. The CLI's entry page is therefore the same bytes
in every build, `launch.html`, and its url launcher,
`app` short path and mobile registration open it. Older CLIs wrote the page
as `entry.html` with the build in it; a browser that opened it keeps that
build, so `entry.html` stays only as the same fixed page for older links, and
`ctx.entryURL` names `launch.html`. Use this SDK with a CLI that writes
`launch.html`.

The CLI names the build in the entry's `entry.json` (`{"v": 1, "build": …}`);
older entry pages carry `<meta name="synopkgland-build" content="…">` instead.
The same hash stamps the entry module, App script, CSS and icons with `?v=`,
and the mobile registration's `build`. `ctx.uiUpdate.check()` reads
`entry.json` with `cache: 'no-store'`, then the entry HTML's meta when there is
no pointer, and compares the exact value. An unavailable page or missing
metadata does not announce an update.

The token read uses same-origin cookies, forbids redirects and keeps tokens out
of URLs and storage. After four seconds it sends one parallel token read and
takes the first completed answer. Neither request is aborted; if both stay
pending, boot keeps waiting.

Identity/language display hints are cached for the package and sign-in. A miss
calls `app.status` and mounts directly once it answers. A failure that is not
a refusal (`busy`, `request_timeout`, an unreadable reply or a network error)
is retried after each wait in `ENTRY_STATUS_RETRY_DELAYS_MS` (250, 500, 1000
and 2000 ms, so at most five attempts). The number of attempts bounds the
retries; no clock fails a slow attempt, which keeps the client's own request
deadline.

The boot hands off to a DSM sign-in only when the caller cannot be verified:
no token, an EntryGate refusal (`session_required`, `admin_required`,
`app_privilege_denied`), a status without an identity, or every attempt
failing. With `portal`, it first probes the alias (`/<alias>/`, or the
entry's own alias prefix). An active alias shows the App's branded DSM sign-in
and keeps the query across it, so the boot goes there with the entry's query
(without `launch`) and hash; a hash route such as `#/a/notes/42` also becomes
`?link=/a/notes/42` when `link` is absent, because DSM's sign-in replaces the
hash. An inactive (404) or unreachable alias, or one that has not answered
within `PORTAL_FALLBACK_PROBE_MS` (4 s), sends the visitor to DSM's App start
instead, with the App's launch intent preserved. Without `portal` the boot
goes straight to DSM's App start. The status may return `lang` (DSM's
code, for example `cht`) and `locale` (for example `zh-TW`); missing language
falls back to the browser. These hints never authorize requests: the App CGI
checks the session, entry policy and method permissions each time.

The page keeps its entry URL: a reload opens the entry again, without DSM's
desktop. `rewriteToAlias: true` (with `portal`) restores the earlier
behaviour: after mounting, the SDK probes the alias and shows it in the
address bar only on success; a 404 or failed probe leaves the entry URL, and a
slow probe does not delay mounting. CGI, WebAPI and update URLs stay anchored
to the original entry URL either way.

DSM's sign-in, and DSM's redirect from an alias or a launch link to a url
launcher, drop the entry's query and hash. Before handing off to a sign-in,
the boot keeps the App's route (`link`, or a `#/…` hash route) and `intake`
in this tab's sessionStorage. The next entry boot in the tab that carries
neither restores them once, as `?link=`/`?intake=` in the address and in
`ctx.launchParams`; a hand-off older than `LAUNCH_HANDOFF_MAX_AGE_MS` (30
minutes) or malformed is dropped. A launch DSM forwards to the entry as
`launchParam` (including the per-character form a url launcher receives) is
read into `ctx.launchParams` too; the page's own query wins.

## Links and DSM Apps

`ctx.entryURL` is the App's address to share or bookmark, without a hash: the
short path on DSM's origin (`https://<nas>/<path>`) when the CLI passes
`shortPath` (a `ui.short_paths` entry, DSM 7.2 and later), otherwise the entry
page `/webman/3rdparty/<package>/app/launch.html`, below the alias prefix when
the page is. Add the App's route as `?link=/…`: DSM's sign-in keeps the query
but replaces the hash. An explicit same-origin `entryURL` option wins. The
short path exists on DSM's own server (5000/5001 and DSM's custom domain), not
on an Application Portal's own domain or port.

A full page outside DSM's desktop has no `SYNO.SDS.AppLaunch`. Its browser
host opens DSM's launch link (`/?launchApp=…&launchParam=…`) in a new tab for
`ctx.host.openApp(appId)`, `openFileManager(path)` and `revealFile(path)`
(File Station's `opendir`/`openfile`, with the DSM window's shared-folder path
rule), and for `ctx.portal.openControlPanel()`. A native shell keeps its own
host. `ctx.portal.mountPanel(element)` renders the plain portal panel for UIs
without their own, as in a DSM window.

## Status through the daemon ingress

An App whose manifest declares `daemon.ingress` gets
`daemonIngress: '/synopkgland/<Package>/'` in its generated entry options.
Its `app.status` reads, the first one at boot and the EntryGuard's background
checks through `ctx.call`, then go to the App's daemon at that path on the
entry's origin instead of starting a CGI process. A CGI process reserves its
whole fallback memory budget from the daemon, so on a NAS whose budget is
already held by running workers the CGI can answer `busy` to every status
read, while the daemon answers at once.

The request is the same JSON POST with the same cookie, `X-SYNO-TOKEN`,
same-origin mode, request deadline and abort signal. The daemon runs the same
SDK session, EntryGate and permission checks as the CGI; a reachable daemon
does not make a caller signed in. Its decoded reply is final: a refusal hands
off to DSM, and `busy` is retried at the ingress like a CGI `busy`.

Only a daemon that did not answer sends that read to the CGI: a network
failure, or HTTP 502, 503 or 504 from nginx. A 404 means DSM's nginx has no
such path (an older package, or an address whose server block lacks it), and
keeps every later status read of the page on the CGI. Every other call,
files and HTTP transfers keep the CGI.

The path must be exactly `/synopkgland/<packageId>/` with the case-sensitive
package ID; anything else, a full URL included, throws before any request.
DSM serves it at the origin root, so a portal alias prefix is not added. The
value comes only from the generated options, never from the page URL. Without
`daemonIngress`, `app.status` uses the CGI as before.

## A shared context

`createAppContext(options)` is also available to a generated DSM window. Pass
the window's `host`, `appWindow`, `session`, `daemonIngress`, `fullPage`, `reopen`, `onLaunch`,
`launchParams`, `user`, `executor`, `portalAccess` and `webApi` when present.
Pass `uiUpdate` to retain a window's existing config/hash check when it does
not generate an entry page. `packageId` and `build` are required. `pageURL` is the original absolute URL
of the page; it defaults to `location.href`. `shortPath` and `entryURL` set
`ctx.entryURL` (see above). `requestTimeout` matches
the backend's request deadline in milliseconds. `messages` holds the App's
catalogs by locale, with English fallback and `{name}`/plural formatting.

The builder supplies the JSON client, batch, File Station session carrier,
translations and update check. It remembers a supplied signed-in user's
language hints. Without a supplied host it uses the native host when a valid
main-frame bridge exists, otherwise a browser host. Native cold URL values
are normalized with `nativeLaunchParams`; warm launch events are composed
with the supplied window subscription. Dispose subscriptions on unmount;
`ctx.host.dispose()` also removes all outstanding native/window subscriptions.
Entry boot reports an observed first contentful paint to the native shell.

## Go status handlers

Apps own `app.status`; adding the SDK does not replace an existing handler.
Use the SDK's request-aware helper when registering that route:

```go
api.HandleRequest("app.status", api.AppStatusHandler(packageID, version, nil))
```

Keep `app.status` authenticated in `permissions.json`. The helper reports the
verified caller, package/version and `lang`/`locale`. Its optional
`api.AppLanguage` callback reads the App's trusted DSM language preference;
a nil callback or an empty preference uses the first `Accept-Language`
tag. Browser language is a fallback, not a claim about DSM's saved setting.
An App with additional status fields can return `api.AppStatus` embedded in
its own response, preserving its existing fields.

## Sign-in caches

`createSignInCache(namespace)` stores optional display hints in localStorage.
Use a package-specific namespace. Its API is:

```ts
const cache = createSignInCache('ExampleApp')
cache.set('home', token, {label: 'Example'}, user)
const value = cache.get('home', token, isHomeSnapshot, user)
cache.remove('home')
cache.clear()
```

The validator is a type predicate. A different sign-in/account, invalid data,
or unavailable storage reads as a miss. `clear()` removes only this namespace.
Only a non-cryptographic fingerprint of the token is kept, for display-cache
separation on both HTTP and HTTPS. It must never establish identity or access.
Do not cache credentials or sensitive record contents. The default entry
host's sign-out clears this package's cache; a custom window host must clear
its own cache when signing out.
