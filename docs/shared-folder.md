# Shared Folder permission

Apps that need a shared data folder use `install.sharedFolder` with
`mode: dedicated`, a fixed app-specific `name`, `key` and `permission: ro|rw`.
DSM's native `data-share` resource creates that folder on the package volume and
grants only the declared permission to the package account. Installation needs
no folder choice. Apps and containers keep running as the package account.
Shared data survives uninstall.

The wizard prefills the dedicated folder, never an arbitrary existing share.
An administrator may select an existing folder instead. That field checks DSM
folder metadata and the package account's existing permission before Next is
allowed. System, encrypted-unmounted, immutable, unavailable and unsupported
folders are rejected. Unknown permission results fail closed. A first install
whose package account does not exist cannot verify access to an existing folder:
use the dedicated folder. The wizard does not modify permissions on unrelated
existing folders. Fixed `mode: create` and explicit `mode: select` remain available.

Dedicated-mode upgrades prefill the recorded folder using the localized dynamic
wizard. A malformed retained record requires an explicit valid selection; a
static wizard also requires an explicit selection. Clearing the editable field
chooses the dedicated folder. The installed data is never moved or deleted by
changing a selection.

Compose maps `compose.shares.<key>.select: install` to the same declaration and
permission. Preparation writes the dedicated source on DSM's selected package
volume before resource acquisition. Every start repeats the kernel permission
check as the fixed package account. Permission loss stops startup with the folder,
account, needed RO/RW and grant-or-reinstall recovery in the DSM hook log, localized
in all supported languages. A stopped gateway answers `app_not_running` and points
to Package Center Run or Repair, independently of DSM browser sign-in.

Wizard permission metadata is a preflight, not a grant or a guarantee about child
ACLs or future changes. Runtime kernel checks remain authoritative. Revocation
cannot recall already-open file descriptors. DSM device acceptance of resource
timing and wizard behavior is separate from generated-artifact and unit tests.

DSM mechanism: [Data Share](https://help.synology.com/developer-guide/resource_acquisition/data_share.html)
and [resource acquisition](https://help.synology.com/developer-guide/resource_acquisition/resources.html).

## What the SDK decides and who decides it

| Question | Answer comes from |
| --- | --- |
| Where is the folder now? | `config.json` name, resolved to `/volumeN/<name>` on every call |
| What may the package account do? | The kernel: `access(2)` run by the App's own process (never root) |
| What does the App need? | `Config.Required` (the `permission` of `install.sharedFolder`: `ro` = read, `rw` = write) |
| Is it pending? | Required > granted, and the folder exists |

The kernel answer covers group membership, deny rows and Windows ACL that a DSM
permission listing cannot decide. An unreadable or ambiguous situation never reads
as granted.

States: `ready`, `pending` (an administrator must grant), `missing` (no volume has
the folder: choose another one when upgrading), `ambiguous` (two volumes have a
folder of that name: rename one). Only `pending` is fixed by a permission.

## Go: one status for the page and the daemon

```go
shares := sharedfolder.Config{
	DataDir:  dataDir, // /var/packages/<id>/var
	Required: map[string]sharedfolder.Access{"media": sharedfolder.Write},
}

// Register the SDK handler; authenticated administrators and users may read it.
routes = append(routes, shares.Routes(nil)...)

// Daemon: the path, or an error that says what to ask for.
root, err := shares.Require("media")
if errors.Is(err, sharedfolder.ErrPermissionPending) {
	// err is a *sharedfolder.PermissionError: account, folder, needed and current access.
	log.Printf("waiting for an administrator: %v", err)
}
```

`Require` returns the path only when the account has the access the App declared;
`RequireAccess(key, sharedfolder.Read)` asks for less (or more) for one operation.
`sharedfolder.APIError(err)` turns these errors into API errors with the stable
codes `shared_folder_permission`, `shared_folder_not_configured`,
`shared_folder_not_found` and `shared_folder_ambiguous`. List
`sharedfolder.status` is an SDK-owned authenticated method; do not redeclare it in
App `permissions.json`. Checks refuse root, differing real/effective IDs, or an
explicit `Config.Account` that differs from the current package account. The
interactive API caller is never used as the filesystem identity.

An App with its own storage registry (a Local storage, a library root) keeps the
path in sync with `Sync`:

```go
changed, err := shares.Sync("media", func(path string) error {
	return upstream.SetLocalStorage("/media", path) // the App's own, idempotent call
})
```

`Sync` requires the declared access, calls `apply` when the path differs from the
one last applied (first use, the share moved to another volume, another folder
chosen) and records it only after `apply` succeeds, so a failure is retried on the
next call. The record is `shared_folders_applied.json` in the data directory.

Do not poll permission status. The startup facade below checks once per open App.
`Require` can guard an actual operation; always handle the I/O error too, because
permissions may change or a child path may have a different ACL.

## Runtime-configured paths and actual I/O failures

Register trusted application storage paths with the SDK handler. Neither request
parameters nor the interactive caller select a target or package identity:

```go
shares := sharedfolder.Config{} // runs as the package account, never root
routes = append(routes, shares.Routes(func(ctx context.Context) ([]sharedfolder.Target, error) {
	return storage.PermissionTargets(ctx) // App-owned configuration
})...)

target := sharedfolder.Target{Key: "media", Name: "Media", Path: configuredPath, Required: sharedfolder.Write}
if err := shares.Check(target); err != nil { return nil, sharedfolder.APIError(err) }
file, err := os.OpenFile(filepath.Join(target.Path, "output.txt"), os.O_WRONLY|os.O_CREATE, 0600)
if err != nil { return nil, shares.WrapError(target, err) }
defer file.Close()
```

`Check` uses `access(2)` without creating temporary files. It checks the directory,
not its entire contents, and does not grant access or authorize the caller's
business operation. The App still enforces its own operation permissions.
`WrapError` converts only `EACCES` and `EPERM`; other errors pass through. The
resulting `PermissionError` preserves its original cause for `errors.Is` and can
be returned directly from an SDK API handler. Single and batch responses include:

```json
{"code":"shared_folder_permission","message":"...","details":{"account":"ExampleApp","share":"Media","required":"write"}}
```

These same details drive the startup notice and operation-failure notice. Avoid
returning raw I/O errors to the frontend: they can contain internal file paths.

## Granting

DSM's way, and the only one that needs nothing from the App: an administrator opens
**Control Panel > Shared Folder**, selects the folder, **Edit > Permissions**,
chooses **System internal user** and gives the package account the access the App
needs. `status.account` is that account's name. The settings page shows exactly
these steps with the names filled in, and a button that opens Control Panel.

**One-click grant** (administrators, when the window provides `ctx.webApi`, DSM's
WebAPI in the signed-in user's own session): the page calls
`SYNO.Core.Share.Permission` `set` (v1) for that one account row with
`user_group_type: "system"`, in the administrator's session, exactly as Control
Panel does. Nothing runs as root and the App's backend never changes an ACL.
`grant` then asks the backend again: only the account's measured access counts, so
a DSM reply that did not take effect shows as `shared_folder_grant_unverified`. A
folder whose account row is an advanced (Windows) ACL is refused
(`shared_folder_custom_acl`) before anything changes. Where the window does not
provide `ctx.webApi`, or DSM refuses, the steps above remain.

The `system` row type and the `set` parameters follow the Control Panel dialogs and
captured `local_user` requests; one-click grant has not been exercised on a NAS.
Treat the guided path as the supported one until a device run confirms it.

## Vue

For a silent, nonblocking check once per open App, create one stable facade and
mount the dialog inside the existing `AppRoot`/`AppShell`. Use its `call` for
operations, or call `permission.handle(error)` from an existing catch block:

```vue
<script setup lang="ts">
import {createSharedFolderPermission, type Context} from '@synopackageland/sdk'
import {SharedFolderPermissionDialog} from '@synopackageland/sdk/vue'
const props = defineProps<{ctx: Context}>()
const permission = createSharedFolderPermission({
  call: (method, params, options) => props.ctx.call(method, params, options),
  locale: props.ctx.locale,
  openControlPanel: () => props.ctx.host.openApp('SYNO.SDS.AdminCenter.Application'),
})
// await permission.call('library.save', params) // still rejects after notifying
</script>
<template><SharedFolderPermissionDialog :permission="permission" /></template>
```

While loading, when ready, or if status cannot be read, the dialog renders
nothing. A denied share opens it with the package account, required access and
Control Panel steps. Dismissal does not recheck; a later real permission failure
can open it again. No polling, timers or automatic grants are added. Unmount
unsubscribes from notifications. Calls keep their original rejection so Apps can
finish their own error handling. `sharedFolderPermissionMessage` exposes the same
localized instruction text for custom UI.

The dialog's **Retry** button explicitly requests fresh status after a native DSM
permission change. Concurrent retries share one request. A denied response keeps
the dialog open; a failed request shows a localized error; a ready response clears
the notice unless a newer real I/O failure arrived during the check. Custom UIs
can use `permission.retry()` for the same behavior. Retry does not start services.

For an explicit settings section, the existing panel remains available:

```vue
<SharedFolderSettings :shared="sharedFolderAccessFor(ctx)" :locale="ctx.locale" />
```

The section loads without blocking the page (one placeholder row), lists every
folder with its state and needed/current access, and adds a notice only for a
folder that needs attention. It renders nothing when the App configures no folder.
`createSharedFolderAccess` takes the same pieces explicitly for a plain UI.

## End to end

1. **Choose.** `dsm-package.yaml` declares
   `install: {sharedFolder: {mode: select, key: media, permission: rw}}`. The
   wizard offers the NAS's folders; postinst stores the name in `config.json`.
2. **Grant.** The page shows `Media` as *Permission needed* with the account name.
   An administrator grants in Control Panel (or one click). The row turns *Ready*.
3. **Use.** The daemon calls `shares.Require("media")` before each run of work. A
   missing grant returns a `PermissionError`; once granted, the next call returns
   the path.
4. **Change.** Upgrading with the change box checked stores another name; the new
   folder is pending until granted, and `Sync` hands its path to the App's storage
   once it is. The old folder keeps the old grant: remove it
   in Control Panel if it is no longer wanted.
5. **Move.** Moving the share to another volume changes `/volumeN` only. The next
   `Status` or `Require` resolves the new path; the DSM permission follows the
   share. An App that keeps the absolute path in its own storage settings calls
   `Sync` from its daemon loop or status route: it applies the new path once and
   does nothing while the path is unchanged.
