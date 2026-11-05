# @synopackageland/sdk

Frontend SDK for DSM packages built with [synopkgland](https://github.com/synopackageland/syno-pkg-land-cli).
Its version always equals the `github.com/synopackageland/syno-pkg-land-sdk` version in the
App's `backend/go.mod`.

| Import | Contents |
|---|---|
| `@synopackageland/sdk` | Headless: `Context` and the other types, `createBackgroundSaveFacade`, `createNasProvider`, `scopedPicker`, `createPortalAccess`, copy catalogs (`uiLocale`, `saveCopy`, …). Never imports Vue. |
| `@synopackageland/sdk/vue` | Everything above plus the components (`AppShell`, `PageActions`, `PageInfo`, `FilesPage`, `NasPicker`, `LoginPage`, `PortalSettings`, `BackgroundPanel`, `ExecutorSettings`, `ExecutorApproval`, `ExecutorNotice`, `UiUpdateNotice`, `DelegatePanel`, `BackgroundSavePanel`, `BackgroundSaveSettings`, `AuthorizationGate`, `AuthorizationSettings`, `EntryGuard`) and composables (`createSession`, `useExecutorSetup`, `ExecutorSetupKey`, `useAuthorization`). |
| `@synopackageland/sdk/vue/style.css` | The component stylesheet. Import it once next to `@synopackageland/ui`'s theme in the App entry. |
| `@synopackageland/sdk/native` | For a native product with its own pinned root tasks: `ROOT_RUNTIME` (`createRootAccess`) and `AUTHORIZATION_RUNTIME` (`createAuthorizationAccess`), plain ES5 source the product renders into its own DSM window; `AuthorizationGate` shows the result. Generated Apps use the executor instead. Never imports Vue. |
| `@synopackageland/sdk/i18n/<capability>/<locale>.json` | Copy in `en`, `zh-TW`, `zh-CN`, `ja`, `ko`, `de`, `fr`, `es`, `it`. |
| `@synopackageland/sdk/spec/protocol/v1/` | The executor protocol (`errors.json`, `schema/`, `vectors/`), a file copy of the repository's `spec/protocol/v1`. Read it from `node_modules/@synopackageland/sdk/spec/protocol/v1`. |

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

Many Apps can use HTTP. Only Apps needing secure context, pairing or a strict
HTTPS Origin policy should require it. Opt-in Go `api.RequireSecureOrigin`
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
menu (DSM 7.3 and 7.4) names the signed-in user above its items. Wider
layouts keep DSM's banner and menu unchanged.
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

## Outside DSM

A development page, a preview or a test owns the browser page and has no DSM
window. `createStandaloneContext({call})` gives it a full-page `Context`
(`locale: 'en'`, `fullPage: true`, a no-op `reopen`) around the App's own
`call`; any other field overrides. `createStandaloneHost(platform?)` is its
`HostAdapter`: `setTitle` titles the document, `openURL` opens an http(s)
URL in a new tab, `subscribe('window-resize')` follows the browser window,
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
last, after every group (an App's settings), `header` and `fill`. The built-in
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

### Scrolling

The navigation, the page bar, a page's tabs and a settings footer stay in
place; only the content scrolls, so no scrollbar runs past them. A page
scrolls as a whole by default. A page that shows a `SettingsPage` or a
`SectionTabs fill` fills the height instead: the tab strip stays at the top,
the footer at the bottom edge of the window, and only the `SettingsPage` body
scrolls (a tab that is not a `SettingsPage` scrolls under the strip). This
works through the App's own wrappers, such as a template's
`.integration-app > [data-page]`, with no CSS in the App. A page that shows a
settings footer has no space under it, whether or not it sits in tabs.

`fill: true` makes any page fill: the page does not scroll and the content
scrolls itself (a treemap, a table paged by height, a log viewer). `fill: false`
keeps a page scrolling as a whole even when it shows a `SettingsPage`. A filling
shell holds to `100dvh`, so a phone's address bar does not leave a gap. On a
phone the same rules give one scrolling area, with the footer in view.

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

Other shell spacing, including the body bottom padding, stays unchanged.

On a phone, `AppLayout`'s bar shows the current page entry's `title` (or the
built-in page's name), also for a page whose title is otherwise only in the
sidebar. A page with `header: false` names itself: the bar takes the title of
its own `PageHeader`. The shell's `title` prop overrides that phone bar title
without changing the page heading. `header-actions` takes the UI's
`DropdownMenuEntry[]`: the phone bar shows the first action and puts the rest
under More; `@header-action` receives the selected key. These props add no
desktop or DSM window header.

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
  subtitle: drop `description` from the page entries (it is no longer shown).
- An App that set `header: false` only to hide the title, and added its own
  hidden `h1`, can drop both: the shell now names the page itself. Buttons it
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
LoginPage shows it without offering a login form.

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
