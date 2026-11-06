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
 mount: async (root, ctx) => {
  const app = await import('./app.module.js?v=the-cli-build-hash')
  return app.mount(root, ctx)
 },
 // Omit portal when this App has no Application Portal alias.
 portal: {defaultAlias: 'example-app'},
})
```

The generated HTML contains
`<meta name="synopkgland-build" content="the-cli-build-hash">`. The same hash
stamps the entry module, App script, CSS and icons with `?v=`, and the mobile
registration's `build`. `ctx.uiUpdate.check()` reads the entry HTML with
`cache: 'no-store'` and compares the exact meta value. An unavailable page or
missing metadata does not announce an update.

The token read uses same-origin cookies, forbids redirects and keeps tokens out
of URLs and storage. After four seconds it sends one parallel token read and
takes the first completed answer. Neither request is aborted; if both stay
pending, boot keeps waiting. A definite token failure or missing identity hands
off to DSM's App start with the App's launch intent preserved.

Identity/language display hints are cached for the package and sign-in. A miss
calls `app.status` once and mounts directly. The status may return `lang` (DSM's
code, for example `cht`) and `locale` (for example `zh-TW`); missing language
falls back to the browser. These hints never authorize requests: the App CGI
checks the session, entry policy and method permissions each time.

Without `portal`, the page keeps its entry URL. With it, after mounting the App,
the SDK probes the alias and rewrites the displayed address only on success.
A 404 or failed probe leaves the working entry URL. A slow probe does not delay
mounting. CGI, WebAPI and update URLs stay anchored to the original entry URL.
Use absolute package asset URLs in the App; relative assets must also work
when the displayed URL changes to the alias.

## A shared context

`createAppContext(options)` is also available to a generated DSM window. Pass
the window's `host`, `appWindow`, `session`, `fullPage`, `reopen`, `onLaunch`,
`launchParams`, `user`, `executor`, `portalAccess` and `webApi` when present.
Pass `uiUpdate` to retain a window's existing config/hash check when it does
not generate an entry page. `packageId` and `build` are required. `entryURL` is the original absolute URL
before any rewrite; it defaults to `location.href`. `requestTimeout` matches
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
