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

## Compose start and stopped-package entry

The original Compose install-selection path only resolved the wizard name into
`runtime.env`; it did not call `sharedfolder.RequireAccess`. Its installed
`compose/control preflight` checked Container Manager availability only. The
general Vue dialog was opt-in and had no explicit retry, so upstream container
startup could fail before an App UI had a chance to display the SDK notice.

The installed control now reads `compose.lock.json` and saved `config.json`,
checks the declared `ro`/`rw` as the matching non-root package account, and fails
before the generated lifecycle proceeds. The existing CLI emits
`precheckstartstop="yes"` and calls this control in prestart/start/restart;
prestart precedes DSM's resource acquisition. A denial is written through the
existing `SYNOPKG_TEMP_LOGFILE` channel with the exact share, package account and
Control Panel path. There is no invented DSM pending state or success exit for a
service that did not start. DSM may still classify this as a failed start, but
the hook supplies a readable remedy instead of an upstream writability error.

The incoming preinst tool is recognized by directory identity against
`SYNOPKG_PKGINST_TEMP_DIR/compose` and skips the share check. This lets installation
finish before granting access. An installed first start still checks even when
DSM keeps `SYNOPKG_PKG_STATUS=INSTALL` and wizard variables in its environment.

The existing gateway `open.cgi` can serve a localized, dismissible native HTML
dialog while the container/gateway daemon is stopped. It checks the same saved
declaration and verifies DSM entry/admission before disclosing share/account
names. Retry is an explicit read-only request; after a grant it directs the
administrator to start the App in Package Center. It never invokes a package
start API or changes an ACL. This fallback has no App frontend process, so it
uses the browser's nonmodal `dialog` rather than adding a second Vue bundle.
Apps with a frontend continue using the existing SDK/UI Dialog, now with Retry,
single-flight requests and preservation of newer real I/O failures. Neither path
polls or retries automatically.

`sharedfolder.Resolve` also preserves permission errors from an unsearchable
volume. The shared Go status/require paths report pending permission rather than
claiming the share was deleted. No directory existence is claimed when its
parent could not be searched.

## Install wizard boundary and follow-up integrations

DSM's documented [wizard interface](https://help.synology.com/developer-guide/synology_package/wizard/WIZARD_UIFILES_v2.html)
renders JSON (or a Vue 2 custom step on DSM 7.2.2+) before installation. Selected
values become hook environment variables later. It exposes no documented
callback to execute an effective-access check as a newly selected package
account, which may not yet exist. Checking the administrator's session/ACL rows
is not an effective-access proof for that account. The existing CLI's select
intro already warns that an administrator must grant access afterwards. A live,
selection-specific ACL verdict in the wizard is therefore unsupported here;
do not add impersonation, a root helper or a misleading browser-side check.
Confidence: moderate for this limitation of the documented surface; live DSM
wizard behavior is untested.

The [lifecycle documentation](https://help.synology.com/developer-guide/synology_package/scripts.html)
supports prestart when `precheckstartstop=yes`; the
[message channel](https://help.synology.com/developer-guide/synology_package/show_massage.html)
supports reporting hook results through `SYNOPKG_TEMP_LOGFILE`. Confidence: high
for these documented contracts. Exact Package Center presentation and whether a
stopped package's DSM launcher remains available require device acceptance.

No CLI/templates/UI repository changes are required for the existing Compose
hooks or CGI to consume this fix when rebuilt against the SDK. Non-compose Apps
must still register `sharedfolder.Config.Routes(nil)` and mount the permission
dialog; their daemon should call `Require`/`RequireAccess` before storage work.
Default template wiring and a stronger localized wizard instruction can follow
in separate template/CLI changes. The current UI Dialog does not expose a
nonmodal prop; adding that API is a separate UI change if the frontend dialog
must leave all underlying controls interactive while open.

Device acceptance (not run here): install with an existing administrator-only
share; verify a readable prestart denial and no resource/container acquisition;
open the CGI entry and verify the account/share/required access in English and
Traditional Chinese; deny and grant in Control Panel; use explicit Retry and
start in Package Center without reinstall; repeat for read-only access,
revocation, upgrade and repair. Test DSM's actual display of hook messages and
stopped-package launcher behavior before claiming end-to-end device support.
