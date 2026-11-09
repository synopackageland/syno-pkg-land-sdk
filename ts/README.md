# @synopackageland/sdk

Frontend SDK for DSM packages built with [synopkgland](https://github.com/synopackageland/syno-pkg-land-cli).
Its version always equals the `github.com/synopackageland/syno-pkg-land-sdk` version in the
App's `backend/go.mod`.

| Import | Contents |
|---|---|
| `@synopackageland/sdk` | Headless: `Context` and the other types, `createBackgroundSaveFacade`, `createNasProvider`, `scopedPicker`, `createPortalAccess`, copy catalogs (`uiLocale`, `saveCopy`, …). Never imports Vue. |
| `@synopackageland/sdk/vue` | Everything above plus the components (`AppShell`, `PageActions`, `PageInfo`, `FilesPage`, `NasPicker`, `LoginPage`, `PortalSettings`, `BackgroundPanel`, `ExecutorSettings`, `ExecutorApproval`, `ExecutorNotice`, `UiUpdateNotice`, `DelegatePanel`, `BackgroundSavePanel`, `BackgroundSaveSettings`, `AuthorizationGate`, `AuthorizationSettings`, `EntryGuard`) and composables (`createSession`, `useExecutorSetup`, `ExecutorSetupKey`, `useAuthorization`). |
| `@synopackageland/sdk/vue/style.css` | The component stylesheet. Import it once next to `@synopackageland/ui`'s theme in the App entry. |
| `@synopackageland/sdk/entry` | [Full-page entry boot](../docs/entry-page.md), shared App context and sign-in display caches |
| `@synopackageland/sdk/mobile` | [Native mobile actions](../spec/native/v1/README.md#page-integration), launch events and host adapter, with browser fallbacks |
| `@synopackageland/sdk/mobile/shell` | DOM-free native wire types, validators and read-once intake store for shell implementations |
| `@synopackageland/sdk/native` | For a native product with its own pinned root tasks: `ROOT_RUNTIME` (`createRootAccess`) and `AUTHORIZATION_RUNTIME` (`createAuthorizationAccess`), plain ES5 source the product renders into its own DSM window; `AuthorizationGate` shows the result. Generated Apps use the executor instead. Never imports Vue. |
| `@synopackageland/sdk/i18n/<capability>/<locale>.json` | Copy in `en`, `zh-TW`, `zh-CN`, `ja`, `ko`, `de`, `fr`, `es`, `it`. |
| `@synopackageland/sdk/spec/protocol/v1/` | The executor protocol (`errors.json`, `schema/`, `vectors/`), a file copy of the repository's `spec/protocol/v1`. Read it from `node_modules/@synopackageland/sdk/spec/protocol/v1`. |

## Raw TCP protocols

Native Go backends use manifest-declared `ipc.ListenTCP` for raw IPv4 TCP
services. Omitted or `loopback` scope binds `tcp4` on `127.0.0.1`; explicit
`scope: all` opts in to `tcp4` on `0.0.0.0`, exposing the port on every IPv4 interface.
Use the DSM firewall to restrict access to an all-interface port.
See [declared TCP listeners](../docs/raw-tcp-listeners.md) for policy, lifetime
and authentication boundaries.

## DSM session carriers

`createDsmSession({read, write, fetch})` provides DSM session carriers for a
desktop window or an Application Portal page. `ready()` returns the desktop's
current token, or makes one shared, bounded, same-origin request to
`webman/login.cgi?enable_syno_token=yes`. `token()` reads the current carrier.
The optional `write` updates DSM's in-memory session for its own WebAPI client.
Nothing is persisted. Recovery failure leaves the backend's existing
`credential_not_ready` response intact; root and delegate calls still require
both the cookie and token. The CLI embeds this factory from the selected SDK
into every generated native window, including Go-only Apps.

An old page can retain a rejected token after another tab signs in again.
`refresh(rejectedToken)` forces same-origin recovery while sharing concurrent
requests. Failed refresh or a same-token reply retains the existing carrier
for later calls; `refresh()` still returns `undefined` and prevents a replay.
`createClient({sessionRetry: {session, operations}})` opts in to one
retry for trusted `effect: read`, `kind: rpc` operations only. Writes, AI,
streams and batches never replay; the default remains off because the client
cannot infer operation effects. Pass the CLI-generated
`sessionRetryOperations` from `generated/session-retry.ts` as `operations`;
unclassified routes have `kind: unknown` and never retry. Existing `ctx.call`
is not automatically wrapped.
See [HTTPS and session recovery](../docs/https-and-session-recovery.md) for the
API, cancellation/deadline behavior and the CLI skill link.

Many Apps can use HTTP. Standalone identity (`identity: standalone`, including
`init --with login`) requires HTTPS for password sign-in and DSM-cookie reuse:
its session cookie is Secure. Use it only for Apps opened outside a DSM window
(Portal or their own tab); do not add it to a DSM-window App. Other Apps should
require HTTPS only for secure context, pairing or a strict HTTPS Origin policy.
Opt-in Go `api.RequireSecureOrigin`
returns `https_required`; render `identityCopy(locale).httpsTitle` and
`errors.https_required` with instructions to reopen through HTTPS.
`readDsmHttpsURL(ctx, location.href)` discovers `dms_https` through
`readDsmPorts` only for an administrator. Ordinary/unknown users get no
PortInfo request and should see instructions only. No port is guessed.
To preserve the current URL's `launchApp` and `launchParam`, use
`readDsmHttpsURL(ctx, location.href, {keepLaunchApp: true, signal})`.
Other query parameters and fragments are dropped; the default still opens DSM
without launch routing. A third-argument `AbortSignal` remains supported.

## DSM WebAPI

Every CLI-generated DSM window provides
`ctx.webApi(api, method, version, params)`, using the existing window channel
and SDK session recovery in the signed-in user's own session. It resolves DSM
data and rejects DSM refusals as `{code: "dsm_<n>"}`. DSM enforces that user's
permissions. The field remains optional in `Context` for standalone dev servers,
custom hosts and windows built by older CLIs.

```ts
if (!ctx.webApi) throw new Error('DSM window required')
const data = await ctx.webApi('SYNO.Core.Service.PortInfo', 'load', 1, {})
```

Pass WebAPI values, not JSON text: DSM's window channel JSON-encodes every
value itself, so `{path: ['/home/a']}` is right and
`{path: JSON.stringify(['/home/a'])}` reaches DSM as a string (DSM 7.4 answers
401). Where the SDK calls WebAPI itself it sends the session token in the
`X-SYNO-TOKEN` header, never in a POST body, which DSM refuses (119, on 7.1.1 as on 7.4.1); a
native download form, which cannot set a header, puts it in its same-origin
query.

`readDsmPorts(ctx)` already prefers this channel and returns `[]` on refusal,
unavailable data or its request deadline. Call it only for an administrator;
PortInfo can refuse ordinary sessions or close their DSM window. It uses a same-origin token
request only without a window channel. `createNasProvider(ctx)` and executor
scope selection also use `ctx.webApi` for File Station JSON calls, retaining
App admission, validation, cancellation and deadlines. A channel refusal never
retries through fetch. Direct `createDsmFileStation({webApi: ctx.webApi})` callers
can use the same transport. Multipart uploads and form downloads use their
existing binary transfer transport.

## File Station

`openFolderInFileStation(path)` opens DSM's File Station at a folder;
`openFileInFileStation(path)` opens the file's folder with the file selected.
Both call `SYNO.SDS.AppLaunch('SYNO.SDS.App.FileStation3.Instance', {opendir|openfile})`
with the shared-folder path (`/volume1/photo/a` becomes `/photo/a`; pass
`{mount}` when the volume mount is known). They return `false` when nothing
was launched: outside DSM, for an empty path, or when DSM reports File Station
disabled or not launchable. File Station reports no completion and applies the
signed-in user's own permissions.

## Application Portal

The DSM window that synopkgland generates hands a portal App `ctx.portal`. An
App that mounts itself in DSM builds the same access with
`createPortalAccess({appId, defaultAlias, locale, location, call, send, isAdmin, launch, entry})`
and passes it to `PortalSettings`. `call` reaches the package backend's
`portal.status` (and, with `entry` below DSM 7.2, `portal.register` and
`portal.unregister`); `send` is DSM's WebAPI in the signed-in user's session.
Its `messages` are `portalMessages(locale)`, the panel text from
`i18n/portal/<locale>.json` (`portalCatalogs`). It has no plain `mountPanel`.
In `PortalSettings`, enabling the entry also applies the default alias, so one
action makes the App reachable; an alias DSM refuses leaves the registered
entry and the alias form to choose another.
Removing an entry from its own alias page also removes that page's relative API
route. `PortalSettings` confirms this first, then directs the user to open the
App from the DSM desktop and enable its entry in Settings. It keeps this
guidance if the removal reply is lost, without claiming success, and across
settings remounts using the same access. A DSM desktop window keeps the normal
remove/re-enable flow. The check uses the active URL's origin and path, not
`fullPage`, so a DSM standalone launch is not mistaken for an alias page.
The access reads the status once when it is created and keeps the newest one
it read (`lastStatus()`); `PortalSettings` starts from it, so a settings page
opened later shows the section as it is instead of placeholder rows that the
status then replaces. An access without `lastStatus` (an older CLI) keeps the
placeholder.

## Full page

A Portal or standalone launch gives the App the whole page; the DSM window
passes `ctx.fullPage`, `ctx.displayName` and `ctx.appIcon`. `isFullPage(ctx)`
reads it, and with an older CLI that passes no `fullPage` treats everything
but a DSM desktop window as a full page. Pass `ctx` to `AppShell` and a full
page takes the web layout and a device-width viewport, names the App in the
sidebar and shows the signed-in user, administrator or DSM user, at its foot;
a DSM window keeps its window layout and title bar. When DSM's Application Portal
banner visibly names this App, the shell hides its duplicate brand block. It
also hides its default account footer while DSM's account menu is visible;
a custom `sidebar-footer` stays. Detection reads only the containing DSM
standalone window's DOM, with no settings API or administrator permission.
Hidden, missing or unrecognized banner markup keeps the shell's own chrome.
In the UI's mobile layout (an app root 640px wide or narrower, AppLayout
`data-ui-layout-mode="mobile"`) the shell also lays out that banner: it shows
the App icon without the App name and the account as an icon, and the account
menu (DSM 7.3 and 7.4) names the signed-in user above its items. At wider
layouts DSM's banner and menu stay as DSM draws them, except that an App name
which would run under the account button ends with an ellipsis 12px before
it; with less than an icon's width free it shows only the App icon. The name
stays the window's accessible name and, while cut, its tooltip.
On a phone (that mobile layout with a coarse pointer) the page has one top
bar. On one of the tab bar's pages DSM's compact banner is that bar; the
App draws no title row under it (see "Phone top bar"). When the App's own
bar is shown instead (a page outside the tab bar, a sub-page with Back, an
App with `header-actions`, or an App without a tab bar, whose bar holds the
menu button), it takes the
banner's place: the banner is hidden and the App gets its height. The
account then moves into the App's bar (below). That follows what the page
declares, never whether a bar is drawn at that moment, and DSM's account
counts as shown while the shell moves the banner, so hiding the banner never
changes which bar the page has. The banner also hides with
the App's bars when the page scrolls down and comes back with them: it
follows the UI's own hide-on-scroll decision (the UI's shared gesture
velocity and thresholds) and moves at the UI's bar speed, at once with
reduced motion. On a phone the window body is sized once and the banner
lies over its top (stacked above the body, which DSM puts at the same
level), so the banner moves by transform and opacity only: the
App's page scroller keeps its size, nothing writes `scrollTop`, and a
momentum fling keeps going while the banner goes. The App keeps the
banner's height as room above its content (`--synopkgland-portal-room`,
counted in the UI's `--ui-bars-room`), and its content moves into that room
by translate, as it moves under the UI's own top bar. A page whose end is a
docked bar or a settings footer keeps the banner, so the bar stays at the
bottom of the page. Wider layouts, a mouse and a DSM window keep the banner.
The rules ship in `@synopackageland/sdk/vue/style.css`.

An App whose own frame already names it and shows the account (a full-page
web App) can drop DSM's banner with the CLI's `ui.banner: hidden` in
`dsm-package.yaml`. The shell then shows the account, and on a DSM full page a
Sign out button under it that calls `ctx.host.signOut()`, DSM's banner sign-out.
An App with its own `sidebar-footer` offers sign-out itself with the same call;
it answers `unsupported` outside DSM and is absent with CLIs before `ui.banner`.
Keep the banner (the default) when the App relies on DSM for the account menu.
`AppShell` reads only `fullPage` (or `host`), `displayName`, `appIcon` and
`user` (`ShellContext`), so the DSM window's ctx works as it is.

```vue
<AppShell v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="pages" sidebar-storage-key="HelloGo" />
```

## Launch links

A DSM desktop link opens the App's window on one of its pages:
`https://<nas>:5001/?launchApp=SYNO.SDS.<package>.Instance&launchParam=<params>`,
or `SYNO.SDS.AppLaunch('SYNO.SDS.<package>.Instance', {route: '/a/notes/42'})`
from DSM's own JavaScript. The DSM window passes the
App's string values from the launch that opened it as `ctx.launchParams`, and
those of each later launch of the open window to `ctx.onLaunch(fn)`. The
values are untrusted text.

`route` is the convention the helpers read: the App route a portal link puts
after its `#`. `launchRoute(params)` returns it when it is a path from the App
root (one leading `/`, no backslash or control character, at most 2048
characters), else `undefined`. `followLaunchRoute(ctx, navigate)` navigates to
the opening launch's route at once and to each later one's, and returns a
function that stops; outside DSM it follows nothing. Map both sources onto the
same router: a portal or full-page launch reads its own `#` as before.

```ts
// Vue Router with createWebHashHistory: #/a/notes/42 in a portal link, route=/a/notes/42 from DSM.
const stop = followLaunchRoute(ctx, route => { void router.replace(route) })
```

`dsmLaunchURL(origin, packageId, params)` builds the desktop link, encoding each
key and value the way DSM decodes them:

```ts
dsmLaunchURL('https://nas.example:5001', 'HelloGo', {route: '/a/notes/42'})
// https://nas.example:5001/?launchApp=SYNO.SDS.HelloGo.Instance&launchParam=route%3D%252Fa%252Fnotes%252F42
```

DSM replays the opening launch's values when it restores the desktop's windows
at sign-in, so a restored window opens on the same page.

For a verified application ID that differs from the package ID, use
`await openApp({appId, params})` in the DSM page or
`dsmAppLaunchURL(dsmOrigin, {appId, params})` with a known DSM origin.
`createDsmAppLauncher({desktop})` provides an injectable platform adapter.
`requested` acknowledges dispatch only; outside the desktop the launcher returns
`unsupported`. See [DSM App launcher](../docs/dsm-app-launcher.md) for parameter
bounds, the MailPlus Server metadata example, manual fallback and WizardStep
handoff/recheck. Keep credentials out of launch parameters.

`packageCenterUrl(origin?)` is the link that opens DSM's Package Center
(`/?launchApp=SYNO.SDS.PkgManApp.Instance`, or on `origin`), for an
administrator who should install another package. DSM has no link to a single
package. See [offering another package](../docs/install-another-package.md).

## Host events

`ctx.host.subscribe(event, listener)` returns `{status: 'subscribed', dispose}`
when supported. Call the subscription's `dispose()` when its component unmounts;
`ctx.host.dispose()` removes all subscriptions and cancels pending callbacks.
Activation listeners receive no native event arguments. Subscription does not emit an
initial notification.

| HostEvent | Source and meaning |
| --- | --- |
| `window-resize` | The AppWindow resize in DSM, or the browser resize in a standalone host. |
| `window-activated` | The App has returned to the foreground; use it to recheck actual state. |
| `host-theme-change` | Reserved; unsupported without a verified host event source. |
| `host-language-change` | Reserved; unsupported without a verified host event source. |

A DSM desktop host observes its own AppWindow's `activate` event, including
bringing it forward, showing it after minimization and switching back from
another DSM window. It does not subscribe to the shared desktop document's
focus/visibility events. A full-page host observes `visibilitychange` when
visible and `window` focus. DSM standalone pages combine those with AppWindow
activation. `createNativeHost` uses the same WebView events: the native bridge
has no separate foreground/resume message.

Adjacent signals within 50 ms are delivered once, asynchronously. Hidden pages
do not notify; hide/deactivate cancels queued DSM delivery. Disposing a
subscription or its host cancels any pending notification. Later returns can
notify again. This event is a refresh hint, not evidence of external completion
or permission. Keep a manual recheck available. Older CLI-generated DSM hosts
return `unsupported`; rebuild the App with a CLI that implements this event.

```ts
const sub = ctx.host.subscribe('window-activated', () => { void refreshStatus() })
// In the component's cleanup:
if (sub.status === 'subscribed') sub.dispose()
```

## Outside DSM

A development page, a preview or a test owns the browser page and has no DSM
window. `createStandaloneContext({call})` gives it a full-page `Context`
(`locale: 'en'`, `fullPage: true`, a no-op `reopen`) around the App's own
`call`; any other field overrides. `createStandaloneHost(platform?)` is its
`HostAdapter`: `setTitle` titles the document, `openURL` opens an http(s)
URL in a new tab, `subscribe('window-resize')` follows the browser window,
`subscribe('window-activated')` follows the page returning to the foreground,
and the DSM desktop actions answer `{status: 'unsupported'}`. Platform fields
override the snapshot, so a test can stand in for a DSM window
(`createStandaloneHost({host: 'dsm', dsmStandalone: false})`).

```ts
const ctx = createStandaloneContext({locale: 'zh-TW', call: (method, params) => post('/api', {method, params})})
```

## App pages

The default logo and app name return to the first entry in `pages` (including a
hidden entry) through `v-model`. With no pages, activation changes no selection.
Use a matching UI release with brand activation support. Pointer, Enter and Space share the same
native button action; the phone drawer closes on activation.

`@brand` receives the original `MouseEvent`. To replace the default selection,
prevent that event and set the page or call your router:

```vue
<AppShell v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="pages"
  @brand.prevent="page = 'dashboard'" />
```

An observer that does not prevent the event keeps the default first-page action.
This does not change the DSM window title bar or DSM's own Portal header.

`AppShell` lays out the App's own pages too. Each `AppShellPage` has an `id`,
a `title` the App has already translated, and optionally an `icon`, a `group`
label (pages with the same label sit together), a `badge`, a `component` to
render while it is the current page, `hidden` (reachable through `v-model`
but not in the sidebar), `placement: 'bottom'` for a page the sidebar lists
last, after every group (an App's settings), `header`, `fill`, `hideOnScroll` and `phoneTitle`. The built-in
`'status'` and `'background'` may sit in the same list.

### Page title and actions

The sidebar already names the page, so a page shows no title of its own and no
subtitle. A visually hidden `h1` with the page's `title` still names it for
screen readers. The `#actions` slot, `PageActions`, the `#meta` slot and
`PageInfo` sit in a bar above the page: notes at the start, actions at the
end. The bar stays in place while the page scrolls and shows only while it
holds something. `header: true` shows the shell's `PageHeader` with the title
and puts the notes and actions on its line instead. `header: false` is for a
page that draws its own header: the shell adds no title, and its actions still
go to the bar.

### Bottom accessory

`#accessory="{page, phone}"` forwards a running task bar or mini player to
`AppLayout`. On a phone it sits above the tab bar, stays while the bars hide
on scroll and hides with the keyboard. Elsewhere it occupies the layout's
bottom row. The UI measures its height and keeps the page, FAB and toasts
clear of it. Use a UI release that supports the accessory slot; older
supported UI releases ignore it.

```vue
<AppShell v-model="page" :pages="pages" :locale="ctx.locale" :ctx="ctx">
  <template #accessory="{page, phone}">
    <RunningTaskBar v-if="running" :page="page" :compact="phone" />
  </template>
</AppShell>
```

An omitted or empty slot draws no accessory row. Its content stays outside
the page scroller, so navigating between pages can keep the same task bar.

`accessory-desktop="notices"` puts the same slot content at the top of the
page, after `#notices`, wherever `phone` is false (desktop, a DSM window, a
narrow window used with a mouse, a tablet). A phone keeps it above the tab
bar. The App declares the task bar once instead of in both `#notices` and
`#accessory v-if="phone"`. The default, `bottom`, keeps it in the layout's
bottom row at every width. Moving between the two places remounts it.

```vue
<AppShell v-model="page" :pages="pages" :locale="ctx.locale" :ctx="ctx" accessory-desktop="notices">
  <template #accessory="{phone}"><RecordingBar :compact="phone" /></template>
</AppShell>
```

### Scrolling

On phones, the bars hide as content scrolls down and return on scroll up.
Set `hideOnScroll: false` on an `AppShellPage` to keep them visible for that
page, such as a live recording screen. The shell passes it to its own
`PageContent`; leaving that page restores the default. This works for both
page components and pages rendered through the default slot.

```ts
const pages = [
  {id: 'home', title: 'Home'},
  {id: 'live', title: 'Live recording', hideOnScroll: false},
]
```

To keep the bars visible throughout the App, pass
`<AppShell :hide-on-scroll="false" ... />`. This forwards to `AppLayout` and
applies to every page; a page cannot re-enable hiding while the App disables
it. Omit the prop and page option to keep the UI defaults. These options do
not change desktop or DSM window layouts.

The navigation, the page bar, a page's tabs and a settings footer stay in
place; only the content scrolls, so no scrollbar runs past them. A page
scrolls as a whole by default. A page that shows a `SettingsPage` or a
`SectionTabs fill` fills the height instead: the tab strip stays at the top,
the footer at the bottom edge of the window, and only the `SettingsPage` body
scrolls (a tab that is not a `SettingsPage` scrolls under the strip). This
works through the App's own wrappers, such as a template's
`.integration-app > [data-page]`, with no CSS in the App. A page that shows a
settings footer has no space under it, whether or not it sits in tabs. The
shell's fill rules use no sibling or `:has()` rule that restyles the whole page
when something under it changes, so an App needs no CSS of its own to replace
them.

`fill: true` makes any page fill: the page does not scroll and the content
scrolls itself (a treemap, a table paged by height, a log viewer). `fill: false`
keeps a page scrolling as a whole even when it shows a `SettingsPage`. A filling
shell holds to `100dvh`, so a phone's address bar does not leave a gap. On a
phone, settings pages scroll as one below the native bar, with the footer
sticking at the bottom; the settings body has no scroller of its own.
Other pages that scroll themselves keep their fill layout.

```vue
<AppShell v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="[
  {id: 'downloads', title: t('downloads'), icon: Download, badge: running || undefined, component: DownloadsPage},
  {id: 'settings', title: t('settings'), icon: Settings, group: t('admin')},
  'background',
]" page-width="fill">
  <template #actions="{page}"><Button v-if="page === 'downloads'" @click="add">{{ t('add') }}</Button></template>
  <template #notices><JobStrip v-for="job in jobs" :key="job.id" :job="job" /></template>
  <template #default="{page}"><SettingsPage v-show="page === 'settings'" /></template>
</AppShell>
```

A page passed as `component` cannot fill the shell's `#actions` slot. It puts
its own buttons, such as its refresh, in `PageActions` anywhere in its
template: they show in the page bar (or the page header) before the App's
`#actions` and leave with the page. Outside an `AppShell`, `PageActions`
renders nothing.

```vue
<!-- PackagesPage.vue -->
<PageActions><Button @click="rescan">{{ t('rescan') }}</Button></PageActions>
```

A page's secondary notes take no row of their own: `PageInfo` puts them behind
one info button at the start of the page bar (on the title line with
`header: true`), before the App's `#meta`.
The button is named "Notes (2)" in the shell's language (`label` names it
otherwise); a click, or focus from the keyboard, opens the notes below it from
its start, inside the App's window, and Escape closes them. The default slot
replaces the list. `inline` keeps the button where the page puts it, beside the
control its notes explain, as does a page shown without an `AppShell`. A page
tested on its own provides `PageInfoTarget` as it does `PageActionsTarget`.

```vue
<PageInfo :notes="[t('notes.sizes'), stale ? t('notes.stale') : '']" />
<PageInfo inline side="left" :notes="[t('notes.searchLimit')]" />
```

`navigation="top"` forwards to `AppLayout` and places the App's pages in a
horizontal bar in web and DSM window layouts. The `sidebar-footer` slot (or
the account supplied by `ctx`) follows the navigation; mobile keeps its drawer.
Omit `navigation` to keep the installed UI's default sidebar, or pass
`navigation="sidebar"` explicitly. Sidebar width and collapse preferences are unused in top mode.

```vue
<AppShell v-model="page" :locale="ctx.locale" :ctx="ctx" :pages="pages" navigation="top" />
```

`page-surface` forwards `auto`, `flat`, `card` or `none` to the shell's internal
`PageContent`, for both normal and denied pages. Omit it to keep the installed
UI's default (`auto`). It is independent of `page-width`, `wide-layout`, page
`header` and `fill`. The Vue entry exports the `AppShellPageSurface` type.

Use `page-surface="none"` when the content owns its horizontal spacing and
surface at every width (web, DSM window and mobile):

```vue
<AppShell v-model="page" :locale="ctx.locale" :ctx="ctx" :pages="pages" page-surface="none" />
```

Other shell spacing, including the body bottom padding, stays unchanged. The
space after a page's last row is the UI's `--ui-page-end-space` (24px; only
the safe area while the phone bars are hidden on a fill page).

A page drops that space where its own bar is the bottom edge: a page that shows
a `SettingsPage` footer, and a fill page whose last element is a docked bar.
A docked bar is a UI footer bar (`AppActionBar`) or any element the App marks
`data-ui-page-bar`, at any depth, as long as it is the last element that draws
something (empty and hidden nodes after it do not count). The App needs no CSS
of its own:

```vue
<div class="recorder">
  <TranscriptView />
  <div class="recorder-controls" data-ui-page-bar>…</div>
</div>
```

A page that scrolls as a whole, or whose bar is followed by other content,
keeps the space.

#### Phone top bar

On a phone outside a DSM window, a tab page draws no title row: the tab bar
already shows where the user is. A tab page is a page whose own tab is drawn
in the `#tabbar` and is current, so the tab keys must be the page ids. No App
icon or page name is repeated, and the shell's `title` prop and a page's
`header: true` heading are not shown there (the page keeps a heading for
screen readers). The one top bar holds only the account and the App's
`header-actions`; with neither, the shell draws no bar at all. The navigation
drawer (the tab bar's More) stays headed by the App's name and icon.

A tab page that must stay titled, such as a recording or live screen shown
as a tab while it runs, sets `phoneTitle: true` on its `AppShellPage`: the bar
keeps its title (and a `header: true` heading) as on any other page, so the
App does not draw a title of its own. Omitted, a tab page has no title row.

```ts
const pages = [
  {id: 'home', title: 'Home'},
  {id: 'live', title: 'Recording', phoneTitle: true, fill: true, hideOnScroll: false},
]
```

Every other page keeps the bar as below: a page reached from the tab bar's
More, a page that is not in the tab bar at all (a hidden detail or live page,
even while the App keeps its parent tab marked current), a sub-page (a
`PageHeader` with `back`, which shows Back, its title and actions), an App
without a tab bar and a DSM window. A page with `header: false` names itself
through its own `PageHeader`, which stays.

The account joins that bar where it would otherwise be a second control: on
a DSM full page whose host can sign out (`ctx.host.signOut()`), the bar shows
an account icon, or, with the App's `header-actions`, an account entry after
them under "…". It opens a sheet with the user, administrator or DSM user,
and Sign out. The drawer's account footer is then left out. In DSM's portal
this happens only while the App's bar takes the banner's place; on a tab page
the banner's own account icon remains. DSM's account menu items other than
Sign out (Help) are not offered while the banner is hidden. The bar's height
and icon sizes are the UI's.

Elsewhere, `AppLayout`'s bar shows the current page entry's `title` (or the
built-in page's name), also for a page whose title is otherwise only in the
sidebar. A page with `header: false` names itself: the bar takes the title of
its own `PageHeader`. The shell's `title` prop overrides that phone bar title
without changing the page heading. These props add no desktop or DSM window
header.

`header-actions` takes the UI's `DropdownMenuEntry[]`; `@header-action`
receives the selected key. The phone bar shows one "…" for them. When the
page puts its own menu in the bar (a sub-page's "…"), they join the end of
that menu after a separator instead of a second "…". An entry is a bar
button of its own only with `primary: true`, or when it is the only entry
and has an icon (the account on its own), and only while the page puts no
actions or menu in the bar:

```ts
const phoneActions: DropdownMenuEntry[] = [
  {key: 'share', label: 'Share', icon: Share2, primary: true},
  {key: 'export', label: 'Export'},
]
```

Place an `AppTabBar` in `#tabbar`; the shell forwards this slot to `AppLayout`,
which renders it only in the mobile presentation. Its More tab opens the
shell's navigation drawer and replaces the phone bar's menu button. Without
a tabbar, or with `AppTabBar :more="false"`, the menu button remains available.
The App controls its tabs and selection; bind the tab bar to the same page
model as the shell.

The `default`, `actions`, `meta`, `toolbar`, `notices` and `tabbar` slot scopes
include `{page, phone}`. The `sidebar-footer` and `denied` scopes include
`{phone}`. `phone` is the reactive boolean from the UI's `usePhone()`: true
when the enclosing `AppRoot` is narrow and the pointer is coarse. A narrow
window used with a mouse keeps it false, even though `AppLayout` can show its
mobile navigation there. `app-root` supplies that root inside the shell; when
omitted, use the App's existing enclosing `AppRoot`. A caller outside that
root can use the slot parameter directly.

```vue
<AppShell app-root v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="pages"
  :title="phoneTitle" :header-actions="phoneActions" @header-action="runPhoneAction">
  <template #tabbar="{page: current}">
    <AppTabBar :items="tabs" :model-value="current" @update:model-value="page = $event" />
  </template>
  <template #default="{page: current, phone}">
    <LibraryPage v-show="current === 'library'" :phone="phone" />
  </template>
</AppShell>
```

`AppTabBar` and its item types come from `@synopackageland/ui`. Existing
`#actions` and `PageActions` retain their page bar or page header placement.

Moving to another page starts it at the top; `keep-scroll` keeps the
position. The `toolbar` slot sits above the page (in `AppLayout`'s toolbar,
above the page bar) and `notices` at the top of the page, inside the same
padding, and scrolls with it. `activePage(current, pages)` keeps a selection that still exists and
otherwise picks the first page the sidebar shows. A router drives the shell
through `v-model`:
`:model-value="route.name" @update:model-value="name => router.push({name})"`.

### Moving an existing App to these defaults

- A page that should keep its visible title sets `header: true`. Pages carry no
  subtitle: drop `description` from the page entries (it is not shown).
- An App that set `header: false` only to hide the title, and added its own
  hidden `h1`, can drop both: the shell names the page itself. Buttons it
  moved into its own `#toolbar` `AppToolbar` can go back to `#actions`.
- Remove CSS that passed the height to a settings page, such as
  `.integration-app > [data-page][data-fill] { flex: 1 1 0%; min-height: 0 }`
  and the same rule for its child, and CSS that removed the space under a
  settings footer (`.app-page-content:has(...) { padding-bottom: 0 }`).
- Remove `fill: true` from pages whose content is a `SettingsPage` or a
  `SectionTabs fill`; keep it for pages that scroll their own content, such as
  a split view or a list paged by height. A page that must keep scrolling as a
  whole while it shows a `SettingsPage` sets `fill: false`.

Render the shell immediately, with [`EntryGuard`](../docs/app-entry.md) checking
session/admin access in the background, and pass `loading` while startup reads
run. The sidebar and the page bar show immediately; the page body shows
its loading area and the actions slot waits. With the current CLI/SDK, use
`ctx.batch` for independent startup reads so they share one HTTP request and
App process:

```ts
const results = await ctx.batch!([
 {method: 'app.status'},
 {method: 'greeting.get'}, // include only when this UI needs this read
])
for (const result of results) {
 if (!result.success) showError(result.error)
 else applyStartupData(result.data)
}
ready.value = true
```

`createSession(ctx).batch` adds the same denial/session handling as its `call`.
Headless Apps can use `createClient({endpoint, token, ready}).batch([...])`.
Each result is independent and keeps input order. Transport failures reject the
whole request; a failed method is an error result. Batches have one to eight
JSON methods, cannot contain stream/cookie routes, have no execution-order or
transaction guarantee, and are never automatically retried. Keep dependent
writes as separate calls. Older generated windows expose only `call`; rebuild
with the current CLI/SDK to get `batch`. Existing single calls remain supported.
Polling still starts on its own schedule. See [the protocol](../spec/batch/v1/README.md).

```vue
<AppShell v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="pages" :loading="!ready" />
```

## Background saves in the settings page

`BackgroundSaveSettings` provides one save-folder row per destination key,
with Choose folder and Revoke access on the same row. It has no save history
or app-wide disable control. The headless facade's operations/cancel and
disable/enable methods remain available to applications.

For a one-time background permission check and clear operation-failure dialog,
use `createSharedFolderPermission` with `SharedFolderPermissionDialog`; see
[Shared Folder permissions](../docs/shared-folder.md). Register the SDK's Go
`sharedfolder.Config.Routes` handler and use the facade's `call` (or `handle` in a
catch block) for operations. A successful check renders nothing and never polls.

For install-selected manifests, administrators choose a shared-folder root
or home and enter their user password. One submit approves that scope through
the existing executor root task, reads the protected selection back, and grants
that same root as their save destination through the session-checked API. It
never silently migrates an existing destination on load. Changing scope still
revokes every user's old grant and cancels pending saves; saved files remain.
Ordinary users choose their own folder inside the approved scope through
`NasPicker`, with fresh DSM session and write-permission verification and no
administrator password. Fixed-scope manifests keep their in-scope picker.

```vue
<AppShell v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="[
  {id: 'downloads', title: t('downloads'), icon: Download},
  {id: 'settings', title: t('settings'), icon: Settings, placement: 'bottom'},
]">
  <template #default="{page}">
    <div v-if="page === 'settings'" class="settings-page">
      <BackgroundSaveSettings ref="saves" :sdk="saves" :admin="ctx.user?.isAdmin ?? false" :locale="ctx.locale"
        :picker="{provider, keys: ['save']}" @destinations="states => folder = states[0]" />
      <SettingsSection v-if="ctx.user?.isAdmin" :title="t('engine')"><!-- the App's own rows --></SettingsSection>
    </div>
  </template>
</AppShell>
```

- `picker.keys` are the App's destination keys, one row each; `labels`
  names the rows when there are several. `provider` is the App's
  `NasProvider` (File Station shares, say); the section scopes it to the
  allowed root.
- `@destinations` reports each key's `DestinationState` after every read, so
  the App knows where saves go without its own `sdk.destination(key)`.
  `@authorized` reports a folder just chosen; `@confirmed` an approval or a
  scope change the executor applied.
- The approval reads `delegate.status` and `bg.browse` with `ctx.call`, or
  with the provided setup's `call` (`ExecutorSetupKey`) when no `ctx` is
  given; an App whose routes differ provides its adapter there.
- A scope change whose root task lands after the DSM window stops waiting is
  read back for 60 seconds before it is reported as failed.
- `refresh()` (exposed) rereads the section, for the page header's refresh.

`BackgroundSavePanel` wraps this section with an opt-in `sampleSave` producer
for templates and samples. It does not list save history or infer completion
from the acknowledgement of a sample job.

An App that had a background page and a settings page merges them: drop the
background page and its `BackgroundSavePanel`, its own scope settings
(`ExecutorSettings` with its own scope readback) and its own destination
row, and place `BackgroundSaveSettings` first on the settings page, which
moves to `placement: 'bottom'`. `ExecutorNotice` and `ExecutorApproval` stay
as they are.

## App root

`app-root` wraps the shell in an app-local `AppRoot`
(`modal-scope="app"`, `height: 100%`) and a `ConfigProvider` with the
shell's `locale`, so the UI components' own text follows it. In a DSM desktop window (a `ctx` that
is not a full page) its dialogs may leave the window (`dialog-bounds=
"viewport"`), and a press on a floating dialog calls
`ctx.host.activateWindow()`, which brings the window to the front; a full
page and a shell without `ctx` keep dialogs in the App. Use it in place of
the App's own `AppRoot`: app-local roots do not nest.

With `app-root`, the declared `color-scheme` prop (`'light' | 'dark'`) goes
directly to `AppRoot`. Leave it unset in a DSM window: DSM draws the window
light. Only the App's own full page (Portal or its own tab) may follow
`prefers-color-scheme`. Without `app-root`, set the scheme on the App's own
`AppRoot` instead. The denied page and LoginPage draw the `--ui-bg` surface
so their background and text use the same theme.

```vue
<AppShell app-root v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="pages" />
```

## Denied access

`denied` replaces the App's pages with one denial page. Pass the backend
error as `{code}` to distinguish entry policies:

- `app_privilege_denied`: ask an administrator to open Control Panel >
  Application Privileges, select the app and click Edit, then allow the account
  or group. The app name comes from `brandName`, then `ctx.displayName`; without
  either, the message says “this app”.
- `admin_required`: the app requires membership in the administrators group;
  open it with an administrator account. An admin-only app is not listed in
  Application Privileges.

`appRefusal(error, locale, displayName?)` supplies the same title and message
used by AuthorizationGate, session denial, and delegate/NAS error copy, in every
SDK locale. Unverified DSM menu translations use the English labels.
Other error codes and `denied: true` retain the generic `shellCopy` text.
Explicit `title`, `message` and `header: false` still take precedence; the
`denied` slot replaces the message. The sidebar, notices and default slot are
not rendered while denied. `createSession(ctx).denied` exposes the entry refusal
with this copy, including refusals during session discovery or later calls.
LoginPage shows it without offering a login form. On HTTP, LoginPage shows a
dismissible warning explaining why HTTPS is required, with a button when
`readDsmHttpsURL` can discover a trusted DSM HTTPS address through the session
context. Otherwise it gives instructions to reopen DSM over HTTPS and ask an
administrator for the address. Both password sign-in and Continue with DSM
remain disabled on HTTP, including after dismissing the warning.

`createSession(ctx).start()` reads `identity.status`. Only a successful
`{enabled: true}` turns on the App's own sign-in (`enabled`). `unknown_method`,
`session_required` and entry refusals keep the DSM shell. Any other failure
(timeout, 5xx, network, `business_unavailable`, package restarting) keeps
`enabled` false, sets `unavailable` and `notice: 'unavailable'`, and re-probes
after 2, 5, 15 and 30 seconds (`retryDelays` overrides); `retry()` probes again
at once. The backend still enforces its own session in the meantime. LoginPage
renders the account form only while `enabled` is true and offers Retry while
`unavailable`. An App that uses only the DSM session passes
`createSession(ctx, {identity: false})` to skip the probe.

```vue
<AppShell v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="pages" :denied="accessError && {code: accessError.code}" />
<AppShell v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="pages" :denied="login.denied.value" />
```

Headless error helpers accept an optional app display name after the locale:
`errorCopy(error, delegateCopy(locale), locale, displayName)`,
`nasErrorCopy(error, nasCopy(locale), locale, displayName)`,
`saveError(error, saveCopy(locale), 'select', locale, displayName)` and
`backgroundErrorCopy(error, backgroundCopy(locale), locale, displayName)`.

## Executor approval

`ExecutorApproval` shows its default slot, the App's pages, only while the
provided executor (`ExecutorSetupKey`) reads `ready`. Until then it takes their
place in the window's content, not in a dialog: an administrator approves with
the user password (`ExecutorSettings`: the form, the status and the refusal's
reason) and the pages show once the executor is ready; anyone else is told to
ask an administrator. A later status that is not ready brings it back, and
with `ctx` so does one read when the window reopens. Without a provided
executor (a dev server, a Portal page) the pages show at once. On a full page
(`ctx.fullPage`, or a standalone host) the approval names the App with
`ctx.displayName` and `ctx.appIcon`, as a DSM window's title bar does. An
approval after which the executor is not ready says it could not be completed,
with the reason the status carries; a password DSM refuses (`dsm_3121`) says
the password is incorrect. Either stays, whatever later status reads say,
until the administrator submits again, refreshes or closes the dialog.
`AuthorizationGate` cannot approve the executor: its `request()` takes no
password.

The approval explains itself before it asks for the password, in one card:
the App is not Synology's and DSM runs no third-party App with system
privileges, then what the App needs them for. A refused password shows at its
field and keeps what was typed. "About this approval", closed at first, says
whose password it is and what approving does: only the package's declared
tasks, a new approval when an update changes them, and removal on uninstall.
`purpose` (on `ExecutorApproval`, `ExecutorNotice` and `ExecutorSettings`)
replaces the generic "Some of its features need system privileges." with the
App's own sentence, in its locale. `ExecutorSettings` and
`BackgroundSaveSettings` show the same explanation inline, and the notice's
dialog under the title "System access".

```vue
<script setup lang="ts">
provide(ExecutorSetupKey, ctx.executor!)
</script>
<template>
  <ExecutorApproval :admin="ctx.user?.isAdmin ?? false" :locale="ctx.locale" :ctx="ctx">
    <AppShell v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="pages" />
  </ExecutorApproval>
</template>
```

With `preload` the App's pages mount while the first status is read, so
their own reads (`authorization.status`, `session.get`, the first page's data)
run alongside it instead of after it. They stay hidden (`visibility: hidden`,
`inert`, `aria-hidden`) behind the loading text until the status reads
`ready`, which shows that same instance with what it loaded; any other status
unmounts them and discards it. Nothing is shown before the check: every read
is still answered by the App's backend under its own authorization. Use it
for pages whose mount only reads: a page that writes on mount would do so
before the check ends, and also when it then fails. A dialog or a toast
leaves the hidden wrapper: `useExecutorPreload()` is `true` while the pages
are preloaded, so a page waits for `false` before it opens one.
`AuthorizationGate` does so with its own dialog. When the status is not
ready it turns `false` before the pages unmount, so `false` does not mean the
pages show: open a teleported dialog from `watchPostEffect` (or after the
next render), when an unmounted page no longer runs it.

```vue
<ExecutorApproval preload :admin="ctx.user?.isAdmin ?? false" :locale="ctx.locale" :ctx="ctx">
  <AuthorizationGate :ctx="ctx"><AppContent /></AuthorizationGate>
</ExecutorApproval>
```

A delegate App keeps its pages while its executor waits for approval (after
an upgrade that changed its policy, say): only what needs the executor fails,
with `EXECUTOR_UNAVAILABLE`. `AppShell` given a `ctx` with `executor` shows
`ExecutorNotice` above every page while the executor reads
`needs-admin-approval` or `blocked`: an administrator approves in a dialog
(`ExecutorSettings`), anyone else is told to ask one, and nothing is held
back. It checks on mount, on each status the executor publishes and when the
window reopens. An App without `AppShell` places
`<ExecutorNotice :ctx="ctx" :locale="ctx.locale" />` above its pages. A
root-ops App keeps `ExecutorApproval`; inside it the notice never shows.
The notice uses the setup the App provides (`ExecutorSetupKey`) before
`ctx.executor`, and that setup's optional `call` before `ctx.call`: an App
whose routes for the approval's reads (`delegate.status`, `bg.browse`)
differ provides its adapter there, and the dialog reads as its settings
page does.

A full page, or a window DSM left open, keeps running the UI it loaded across
a package upgrade until the page reloads; windows opened after
the upgrade load the new UI themselves. The CLI's DSM window gives `ctx.uiUpdate`: its `check()` reads the package's `ui/config`
without the browser cache and answers whether the installed build replaced
the window this page loaded. `AppShell` given that `ctx` shows
`UiUpdateNotice` above every page once it answers yes: one line, Updated.
Reload the page, with a **Reload** button, and nothing held back. It checks on
mount, when the window reopens and when the page is shown again (at most once
a minute). A failed check, or a window without `uiUpdate`, shows nothing. An
App without `AppShell` places `<UiUpdateNotice :ctx="ctx" :locale="ctx.locale" />`
above its pages.

The backend's `executor.status` and `executor.control` are the SDK's: a Go
App registers `delegate.ExecutorRoutes`, and the bridge runtime serves them
with `capabilities.root`.

## Calling the backend

`ctx.call(method, params, {signal})` posts to the package backend. An abort of
`signal` rejects it with `{code: 'request_canceled'}`, for a page the user
leaves; past `clientTimeout(ctx.requestTimeout)` it rejects with `{code:
'request_timeout'}`.
Without `ctx.requestTimeout` the deadline is `DEFAULT_REQUEST_TIMEOUT`, 300
seconds like the backend's, and the client waits 302. Explicit App deadlines
still take precedence; set the backend and browser together.

For NAS work of uncertain duration, submit to the existing background worker,
return its original job ID and poll authorized status/result endpoints. Show
queued/running as still working, optionally labeling a long wait as slow; do not
infer failure from elapsed time or a failed status request. A timeout after a
mutation was sent leaves its completion uncertain: read back the original job
or operation, without automatically submitting a new one. See
[request deadlines](../docs/request-deadlines.md).

```ts
const controller = new AbortController()
const items = await ctx.call<Item[]>('items.list', {}, {signal: controller.signal})
```

## Localized copy

For UI/locale copy we author, remove the final sentence-ending period in every
language, including `.` and `。`, even in multi-sentence text; keep periods
between sentences. Preserve question marks, exclamation marks, ellipses,
abbreviations (`etc.`), version numbers, URLs and trailing `{key}` placeholders.
This covers the SDK, UI package, templates, our Apps and UI copy we add under
an upstream fork's `synology/` (such as DSM install wizards and gateway pages).
Leave upstream UI/locale strings and release notes unchanged; future checks
must scan only our owned files. See the CLI skill's
[UI copy and i18n rule and examples](https://github.com/synopackageland/syno-pkg-land-cli/blob/main/skills/reference/ui-vue.md#ui-copy-and-i18n).

`format(message, values, locale)` fills `{key}` placeholders from `values`; a
placeholder with no value stays visible. A message may be plural forms keyed by
the `Intl.PluralRules` categories (`zero`, `one`, `two`, `few`, `many`, `other`;
`other` is required), chosen by `values.count` in `locale` (a DSM code such as
`cht` or a BCP-47 tag).

```ts
import {catalogs, format, mergeCatalogs} from '@synopackageland/sdk'
const copy = mergeCatalogs(sdkCatalogs, import.meta.glob('./i18n/*.json', {eager: true, import: 'default'}))
format(copy[locale].files, {count: 3, folder: 'photo'}, locale) // {"one": "{count} file in {folder}", "other": "{count} files in {folder}"}
```

`catalogs` and `mergeCatalogs` fill every key a locale lacks from `en`.
`mergeCatalogs` layers the App's catalogs over the SDK's, key by key; for each
key the order is the App's locale, the SDK's locale, then `en`.

`bridgeErrorCopy(error, locale)` is the text of a code that the bridge runtime
adds for Python, Node and Rust Apps: `app_unavailable`, and the services'
`limit_exceeded`, `read_failed`, `identity_unavailable` and
`credential_not_ready` (a request without DSM's SynoToken); and of
`request_timeout`, which a Go App and the runtime send past the request
deadline and an SDK client gives when it stops waiting. For any other code it
returns `undefined`, so the App falls back to its own text. Its catalogs,
`i18n/bridge/<locale>.json`, are generated from `spec/bridge/v1/errors.json`.

`appLifecycle(error)` reads a refusal or a status that names a stopped or
starting package (`APP_STOPPED`, `APP_STARTING`, `EXECUTOR_STARTING`, or the
DSM window's `executor_starting`) and returns `'stopped'`, `'starting'` or
`undefined`. `lifecycleCopy(lifecycle, locale)` is the text the SDK's panels
show then (9 locales). Such a package answers again by itself: show the text
as information, not as an error, and read again after
`lifecycleRetryDelay(attempt, lifecycle)`: 2 s, doubling to
`LIFECYCLE_RETRY_MAX_MS` (15 s) while stopped and to
`LIFECYCLE_STARTING_RETRY_MAX_MS` (4 s) while starting. Start `attempt` over
at 0 when the lifecycle changes, so a start is seen soon after it ends.

```ts
import {appLifecycle, lifecycleCopy} from '@synopackageland/sdk'
try { jobs = await ctx.call('jobs.list') } catch (error) {
  const lifecycle = appLifecycle(error)
  if (lifecycle) notice = lifecycleCopy(lifecycle, ctx.locale) // and poll again
  else throw error
}
```

Pick the text by what happens next:

| The refusal came from | Use | Says (zh-TW) |
|---|---|---|
| A read the view repeats on its own (a list, a status), as above | `lifecycleCopy(lifecycle, locale)` (`for: 'page'`, the default) | 此 App 已停止。請在套件中心啟動，啟動後此頁面會自動更新。 |
| An action the user started (submit, save, delete) that nothing sends again | `lifecycleCopy(lifecycle, locale, {for: 'action'})` | 此 App 已停止。請在套件中心啟動後再試一次。 |

The page text promises that the page updates by itself, so use it only where
the view polls again. A refused action keeps its input (the link stays in
the dialog) and says to try again:

```ts
try { await ctx.call('downloads.add', {url}) } catch (error) {
  const lifecycle = appLifecycle(error)
  if (lifecycle) dialogError = lifecycleCopy(lifecycle, ctx.locale, {for: 'action'})
  else throw error
}
```

The Vue entry needs the peer dependencies `vue`, `@synopackageland/ui` and
`@lucide/vue`. The headless entry needs none.

## Development

```
npm ci
npm test
npm run typecheck
npm run build
```

`npm run test:browser` measures the NAS folder picker in headless Chromium
(`test/browser/picker-layout.mjs`): loading must not move the dialog, the tree
or the list, and nothing scrolls at 1280px or 768px. It needs `npm i --no-save
playwright-core@1.58.2` and a Playwright Chromium (`CHROMIUM_PATH` selects
one); `--shots DIR` saves screenshots.

`jsdom` must stay at 29 or newer: the UI's `AppRoot` modal-scope mirror lists
`[...element.style]`, and jsdom has made `CSSStyleDeclaration` iterable (the
CSSOM behaviour browsers have) only since 29.

After a change to `../spec/bridge/v1/errors.json`, run `node
scripts/gen-bridge-i18n.mjs`; `npm test` fails while `i18n/bridge/` differs
from it.

`npm run build` also copies `../spec` into `spec/`. Run `npm ci` before
`npm pack`: `prepack` builds and needs `node_modules`. `npm install
--install-links` from a checkout does not run `prepack`, so build first.

`../scripts/gate.sh` runs these and checks the `npm pack` file list. While the
locked `@synopackageland/ui` version is not on npm, set `SYNOPKGLAND_UI_TARBALL`
to its tarball; the gate installs it in place of `npm ci`.

License: MIT.

Protocol v1 includes `WebapiCallParams` for `webapi.call` and profile grants in
`DelegateCreateParams`. `encodeRequest`/`decodeResponse` retain the 64 KiB wire
bound. Node App services use Context.runtime (node/README.md); browser Apps
expose their own app routes and never connect to the executor socket directly.
Go workers use `delegate.CreateWebAPIGrant` and `JobContext.WebAPI().Call`.

## DSM file session migration

`createNasProvider`, `FilesPage`, scope selection and file transfers now call
DSM directly on the browser's origin. The App's `nas.session.status` still
checks admission. `ctx.files.endpoint` is ignored; the reactive token callback
is retained, with a same-origin DSM token bootstrap outside the desktop.
For custom UIs, use the headless `createDsmFileStation` (shares, list, info,
createFolder, checkPermission, upload) and `createFileTransfers`. Await
`download()` for submission failures; it does not confirm transfer completion.
Browser permission results never authorize backend writes or grants.

See [DSM session transport and App migration](../docs/dsm-session-transport.md).
Do not add root/executor access to browser-only Apps such as OpenList.

### DSM service ports

`readDsmPorts(ctx)` returns localized PortInfo definitions on demand in the
current administrator's DSM session. Start it after rendering the App. Refusal,
missing session and timeout return `[]`; use package-account `dsmservices`
reading on the backend independently. See [service ports](../docs/dsm-service-ports.md).

For private native `<audio>` / `<video>` playback, use `mediaUrl(ctx, {asset})`
with the Go media service. It renews a stable, session-bound URL without resetting
playback. See [media grants](../docs/media.md) for backend hooks and cleanup.

## Native mobile shell

SynoPkgNative protocol 1 and the `mobile:` manifest are stable. Optional additions
retain existing behaviour; incompatible changes require a new protocol and shell
negotiation. See the [compatibility policy](../spec/native/v1/README.md#stability-and-compatibility).

`saveBlob` and `saveUrl` sanitize user-provided download names into bounded
Unicode basenames for both native and browser downloads, retaining the final
extension where it fits. See [filename rules](spec/native/v1/README.md#page-integration).
Entry callers can declare `appBundle` to preload their module during identity
requests without executing it; see [entry pages](../docs/entry-page.md).

Registered mobile Apps use `@synopackageland/sdk/mobile` for `saveBlob`,
`saveUrl`, `openExternal`, `routeExternalLinks` and read-once `takeIntake`.
Each action has a browser fallback when the bridge or capability is absent.
`createNativeHost` retains the existing host adapter shape and reports
`native-shell` through `ctx.host.platform().host`. Context builders compose
`onNativeLaunch` into `ctx.onLaunch` and use `nativeLaunchParams` for cold
launch values.

Shell implementations import the DOM-free wire types, validators and intake
store from `@synopackageland/sdk/mobile/shell`. The bridge is injected only
for a registered App at its NAS origin, in the main frame. See the
[wire contract and integration guide](../spec/native/v1/README.md) for
message shapes, transfer events, fallbacks and the shell's trust boundary.

## Native HTTP metadata

The headless entry exports the v3 native HTTP registry, descriptor and inventory
TypeScript types. The formal schemas are available through
`@synopackageland/sdk/spec/api/v3/*`. They describe explicit
`ctl.transport: native-http` bindings for Compose and native Apps; the CLI owns
upstream credentials, HTTP execution and file transfers. The SDK serves validated
metadata through the existing authenticated DSM CGI entry. See the
[native HTTP contract](https://github.com/synopackageland/syno-pkg-land-sdk/blob/main/api/nativehttp/README.md).
