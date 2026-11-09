# Open another DSM application

Use a verified **application ID** with the platform SDK. A package ID is not
necessarily its application ID, and one package can register multiple
applications. See Synology's [Desktop Application guide](https://help.synology.com/developer-guide/integrate_dsm/desktopapp.html)
and [Application Config](https://help.synology.com/developer-guide/integrate_dsm/config.html).

```ts
import {openApp, dsmAppLaunchURL, type DsmAppTarget} from '@synopackageland/sdk'

const target: DsmAppTarget = {appId: 'SYNO.SDS.MailPlusServer.Instance'}
const result = await openApp(target)
// result.status === 'requested' means dispatch only. Recheck application state.

// Optional link, only when configuration supplies the actual DSM desktop origin.
const href = dsmAppLaunchURL('https://dsm.example:5001', target)
```

`openApp(target)` and the injectable `createDsmAppLauncher({desktop})` implement
`DsmAppLauncher.openApp(target): Promise<DsmAppLaunchResult>`. They share the
existing `DsmDesktop` contract and desktop dispatch with File Station navigation.
They feature-detect this page's `SYNO.SDS.AppLaunch(appId, params)` and do not
inspect `parent` or `top`, navigate, or open a popup automatically. An explicit
`{desktop: undefined}` disables global discovery. Tests or an embedding host can
provide a trusted desktop capability explicitly.

The existing synchronous `ctx.host.openApp(appId: string)` is retained for
CLI-generated hosts and existing callers. Do not pass the new target object to
an older CLI's string-only method; use the SDK's `openApp(target)` or
`createDsmAppLauncher()` in the DSM page. The Go CGI and language bridge handle
authenticated application/WebAPI calls, not desktop window operations; no new
backend route or privileged helper is needed to launch a window.

| Status | Meaning |
| --- | --- |
| `requested` | `AppLaunch` was called once without throwing. Its return value is not a completion signal. |
| `unsupported` | This page has no `AppLaunch` capability, including standalone/Portal pages or cross-origin iframes without an injected desktop capability. |
| `unavailable` | DSM explicitly returned `false` from `StatusNotifier.isAppEnabled` or `isAppLaunchable`. |
| `invalid-input` | Validation failed before dispatch. |
| `failed` | Desktop discovery, a status check or dispatch threw. No retry is performed. A dispatch exception cannot prove that no window opened. |
| `blocked` | Reserved for a host that supplies an explicit refusal; the desktop adapter does not infer it. |

Neither launch nor URL generation establishes installation, user permission,
configuration completion or success. Missing status methods are unknown, not a
refusal. Always offer a readable manual path and a way to recheck actual state.

## Parameters and origin

`DsmAppTarget.params` is an optional read-only string record. IDs must start
with an ASCII letter and contain only letters, digits, `_`, `-` and `.`, up to
256 characters. Parameter keys start with an ASCII letter and contain those
same characters, up to 64 characters. There are at most 32 parameters; each
value is at most 2048 UTF-16 code units and contains no C0/C1 control characters.
Malformed Unicode is refused. The inner encoded `launchParam` is bounded to
8192 characters, including keys and separators. These bounds apply equally to
direct dispatch and URL generation.

Parameters are App data, not DSM configuration. `className`, `instanceName`,
`dsm_version` and `title` are refused because DSM interprets them itself.
Common credential keys such as `password`, `SynoToken`, `access_token`,
`Authorization`, `api_key` and `sid` are also refused. The App must never put
secrets under other keys or inside routes. Parameters and URLs grant no access.

`dsmAppLaunchURL(dsmOrigin, target)` takes an explicit HTTP(S) origin, with an
optional trailing `/`; credentials, paths, queries, fragments, backslashes,
whitespace and control characters are refused. Invalid input throws. Pass a
configured DSM origin or a verified host context value. A Portal's
`location.origin` may identify a different service; never assume it is DSM.
If the DSM origin is unknown, omit the link and show the manual instructions.
In a cross-origin iframe, do not probe parent globals to find it.

The builder uses the existing DSM encoding contract: encode each key and value,
join pairs with `&`, then encode that string as the `launchParam` query value.
Punctuation and Unicode round-trip without changing the target.
`dsmLaunchURL(origin, packageId, params)` remains available with its existing
behavior for packages using `SYNO.SDS.<packageId>.Instance`.

## Verify the target

The MailPlus example comes from a supplied read-only metadata observation on
DSM **7.3.2**, Synology MailPlus Server **4.1.1-31785**:

```text
SYNO.Core.Package get id=MailPlus-Server additional=dsm_apps
dsm_apps:
  SYNO.SDS.MailPlusServer.Instance
  SYNO.SDS.MailPlusServer.Extension.Instance
  SYNO.SDS.MailPlusServer.MainWindow
```

`SYNO.SDS.MailPlusServer.Instance` is the high-confidence launch target from
that observation. This SDK change has not opened the application on a NAS.
The observation establishes neither launch behavior on other DSM/package
versions nor any supported MailPlus launch parameters, so the example supplies
none. Do not choose an extension or internal window merely because it appears
in the metadata.

An App may query `SYNO.Core.Package get` with `additional=dsm_apps` through its
existing current-user WebAPI channel, then compare an expected target to the
returned IDs. This release adds no discovery helper or WebAPI profile. In a
backend integration, declare the required read operation using the existing
[WebAPI profile mechanism](../spec/protocol/v1/README.md); never add a root helper for this check.
A refused, missing or malformed metadata response means **unknown**. It does
not mean absent or permitted. Membership alone does not prove launchability.

## WizardStep handoff and recheck

Keep workflow state in the App. The UI uses its existing WizardStep and Button;
the platform SDK performs the external launch. For example, in a setup wizard:

```vue
<WizardStep step-key="external" headline="Complete setup in MailPlus Server"
  next-label="Check again" next-step-key="review"
  :pre-enter-next-step="checkBeforeContinuing">
  <p>Finish setup, then return here and check again.</p>
  <Button variant="secondary" @click="openMailPlus">Open MailPlus Server</Button>
  <p>Or open DSM Main Menu → MailPlus Server.</p>
  <p role="status">{{ handoffText }}</p>
  <p v-if="!configured">Setup has not yet been confirmed.</p>
</WizardStep>
```

```ts
import {ref, onMounted, onUnmounted} from 'vue'
import {openApp, type Context} from '@synopackageland/sdk'

// Receive the context from the App entry.
const props = defineProps<{ctx: Context}>()
const configured = ref(false)
const handoffText = ref('')
let active = true
let pending: Promise<void> | undefined

async function openMailPlus() {
  const result = await openApp({appId: 'SYNO.SDS.MailPlusServer.Instance'})
  if (!active) return
  handoffText.value = result.status === 'requested'
    ? 'Open request sent. Finish setup, then check again.'
    : 'Open MailPlus Server from DSM Main Menu, then check again.'
}

function refreshStatus(): Promise<void> {
  // Coalesce activation and button checks; do not start overlapping polls.
  if (pending) return pending
  pending = (async () => {
    try {
      const state = await readSetupStatus() // App-owned authenticated read
      if (active) configured.value = state.configured === true
    } catch {
      if (active) configured.value = false // Unknown cannot advance the wizard.
    }
  })().finally(() => { pending = undefined })
  return pending
}
async function checkBeforeContinuing() {
  await refreshStatus()
  return active && configured.value ? undefined : false
}
let stopActivation: (() => void) | undefined
onMounted(() => {
  const sub = props.ctx.host.subscribe('window-activated', () => { void refreshStatus() })
  if (sub.status === 'subscribed') stopActivation = sub.dispose
  // An older generated host can return unsupported. Keep Check again available.
})
onUnmounted(() => { active = false; stopActivation?.() })
```

`readSetupStatus()` belongs to the App and must check its own required state
and current user's access. For a mounted wizard that can close/reopen or change
steps, also invalidate or abort reads on close/step changes, and use a generation
check before accepting a reply. Host activation is only a trigger to read
again; `window-activated`, closing an external window, or `requested` must never set
`configured = true`. Keep the manual path visible even after `requested`, since
the SDK has no external completion callback. Ordinary users can receive an
explicit unavailable result or an unknown read; keep the instructions and
recheck available without claiming success or escalating privileges.

The [HostEvent contract](../ts/README.md#host-events) describes activation sources
and cleanup. DSM desktop windows need a host generated by a CLI that supports
this event; updating the frontend dependency alone does not update an installed
host script. A browser `window` focus listener cannot detect switching between
DSM Apps sharing the same desktop document.
