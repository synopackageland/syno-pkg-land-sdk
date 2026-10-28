# App entry policy

Declare `entry: admin-only` in `dsm-package.yaml` to admit DSM administrators
through the generated CGI's existing `dsm.NewEntryGate`. Session-authenticated
App and SDK handlers share that decision, including batches and HTTP transfers.
Failed or unavailable session/admin checks refuse access before these handlers
run.

Public App routes also pass entry admission in `api.Server`. Only the SDK's
four fixed identity ceremony operations remain public so a browser can establish
or dispose of its session. They do not dispatch App code.
The frontend check does not set backend policy.
Omitting `entry` preserves the route policy's access.
`entry: app-privilege` lets DSM Application Privileges admit ordinary accounts,
with its public identity sign-in routes accessible. Business route permissions
still apply after entry admission.

The package process remains the unprivileged package account. An administrator
session grants App access; it does not change the process UID or grant root.
Backend entry policy is the authority. `ctx.user` only controls presentation.

The CLI binds the entry mode and both App/SDK permissions digests into the
packaged runtime. A missing Go wrapper cannot remove the administrator floor;
an application-privilege build refuses startup without a matching entry
authenticator. The CLI rejects contradictory App permissions and unlisted
registrations before packaging. See [the access-path audit](app-entry-access.md)
for dispatch paths, migration requirements and trust boundaries.

## Background frontend check

Render the page immediately and place `EntryGuard` inside the existing
`AppRoot`. It can wrap `AppShell` or stand beside it.

```vue
<script setup lang="ts">
import {EntryGuard, AppShell} from '@synopackageland/sdk/vue'
</script>

<template>
  <EntryGuard :ctx="ctx" mode="admin-only" :locale="ctx.locale">
    <AppShell v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="pages" />
  </EntryGuard>
</template>
```

The component calls `app.status` in the background on mount, window reopen and
page visibility. The status route must return the SDK's `AppStatus` shape with
the backend-verified `user` and boolean `admin`. Configure the backend entry
policy to match the component's `mode`; the frontend does not set that policy.
It never trusts cached `ctx.user` for admission.

A successful check adds no loading screen or dialog. A failed session, entry
refusal or unavailable check opens a dismissible dialog with Retry while the
page stays mounted. Backend calls continue to enforce access independently.
Dialog copy uses the SDK's existing identity and authorization catalogs in
`locale`; pass `messages` to supply App-specific text and button labels.
Concurrent checks coalesce; unmount aborts the request and removes listeners.

For a custom UI, use the headless `createEntryCheck(ctx, mode, onResult)`, call
`check()` without delaying the page's render, and call `dispose()` on cleanup.
`onResult(undefined)` clears a failure. Structured failures distinguish
`session_required`, `admin_required`, `app_privilege_denied` and
`auth_unavailable`.
