# Shared Folder permission checks

## Native DSM grant first

DSM's [Data Share worker](https://help.synology.com/developer-guide/resource_acquisition/data_share.html)
can grant `ro` or `rw` access to a named package account, including when the share
already exists. Prefer the existing `install.sharedFolder: {mode: create, ...}`
resource declaration for a fixed share name. The worker runs at package startup;
the share and grant are retained on uninstall. This is DSM resource management,
not a privileged SDK process.

The current CLI emits that declaration only for `mode: create`. A folder selected
by a user later is not a fixed resource. The current DSM guide lists no worker
environment variables and marks this resource non-updatable. It does not document
a robust runtime-selected-name grant. Do not rewrite `conf/resource`, elevate a
process, or invoke an ACL tool. The older DSM 6 guide describes wizard input, but
that alone does not establish a supported DSM 7 dynamic integration. Confidence:
high for the documented fixed-name worker, unknown for dynamic wizard substitution
and device-specific ACL outcomes. No NAS validation is part of this change.

## Incremental implementation

- Extend the existing Go `sharedfolder` package. Keep `Status`/`Require` for wizard
  configuration and add checks for trusted application-configured paths, so the
  feature works for every App, including storage selected after installation.
- Use `access(2)` under the running package identity; do not create files or scan
  contents. Refuse root, unequal real/effective IDs, and a configured account that
  differs from the process account. Never impersonate the interactive API caller.
- Reuse `PermissionError` for preflight and wrapped `EACCES`/`EPERM` failures. Keep
  the original cause available to `errors.Is`. Preserve unrelated I/O failures.
  A successful root check is advisory: child ACLs and later changes can still fail.
- Add `SharedFolderPermissionDialog` to the SDK Vue entry, composed from the
  existing UI `Dialog` and buttons. It starts one asynchronous status request on
  mount, renders nothing while loading or when ready, and opens only for pending
  permissions. Dismissal never retries. No timers, polling, grants, or blocking
  wrapper. Keep the existing settings panel for explicit settings interactions.
- Use the existing localized instruction catalog for account, share, required
  access, Control Panel path and System internal user hint. English is the fallback.
- Provide a template integration example using the backend process identity and a
  stable frontend facade. Keep CLI free of runtime logic and preserve existing UI
  primitives. Add a focused templates example without changing default scaffolds
  to require an unreleased SDK.

## Validation

TDD covers allowed/denied read/write, root and identity refusal, runtime-configured
paths, wrapped permission errors and unrelated errors; Vue checks cover one request,
silent success, multiple denied shares, dismissal and unmount during a request.
Run the affected SDK fast gate once after focused tests. Frontend checks may run
locally. Do not build Rust, touch a NAS, merge or release.

The first implementation covers the Go core and TS/Vue frontend. Other language
SDKs can later use the same Go runtime routes rather than duplicate permission
logic. The SDK owns the status handler; Apps only register trusted target paths.
Operation errors carry `shared_folder_permission` with structured `details`:
`account`, `share`, `required` (`read` or `write`). The frontend facade forwards
these failures to the same dialog used for startup and still rejects the call.
