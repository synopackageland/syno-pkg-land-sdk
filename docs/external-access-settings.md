# External access settings

External access requires SDK 0.29.0 or later. A Compose App must use that
version for both `compose.sdk` and `identity_gateway.sdk`.

## DSM settings page

Open the package's settings CGI from the DSM origin:

```text
https://<NAS>:<DSM port>/webman/3rdparty/<package>/sso/settings.cgi
```

The first GET only obtains a SynoToken. It does not read or display saved
addresses. A same-origin POST verifies the DSM session, SynoToken and native
administrator role before showing the settings. Every save repeats those
checks. The App origin has no settings write endpoint.

The page lists the local App address, effective manual and Connect public
addresses separately, and the effective DSM address. Private addresses are
viewed in Connect; its public projection intentionally contains no private
network addresses. The page also shows the App's configured sign-in policy,
passthrough exceptions and the two-step verification recommendation.

Only `manual_origin` and `dsm_origin` can be edited. An empty field clears the
override. Enter literal HTTPS DNS origins without a path, query, fragment,
credentials or a non-default port. `:443` and hostname case are normalized.
An App public origin cannot equal the effective DSM origin.

Settings are stored at `<StateDir>/external.json`, owned by the package account
with mode 0600. Each successful save, including an unchanged value or a clear,
increments the persistent revision. Saving a stale revision returns 409 and
requires reloading. Concurrent saves are serialized. A save writes a temporary
file, syncs it, renames it and syncs the parent directory.

For `external.origin_change: confirm`, changing the primary origin requires
explicit confirmation because passkeys and sign-in may be affected.

## Compose environment

Declare the host-resolved App origin in a service's existing runtime environment:

```yaml
storage_init:
  directories:
    - base: /data
      path: app
  runtime:
    environment:
      DOMAIN:
        external_origin: true
```

`external_origin` is mutually exclusive with `setting`, `secret` and
`oidc_client`. It does not require an `install.settings` entry. The service
still needs the normal nonroot storage initialization and explicit argv.

At package preparation, the host uses the shared `EffectiveExternal` resolver:
manual, then Connect, then the App's configured `public_origin`. It atomically
writes a mode-0600 snapshot at `target/compose/settings/external-origin.value`.
The planner mounts only that file, read-only, at
`/synopkgland/settings/external-origin.value`. Containers receive neither gateway
state nor the Connect projection. Multiple environment keys share one mount.

With no effective value, the snapshot is empty and the runtime leaves the
environment key unset, including removal of an inherited image value. An
origin is not a required installation setting. The empty file keeps the bind
source valid without Docker creating a directory in its place.

The snapshot is refreshed when the App is prepared for startup. Restart the
App after changing its address to update its container environment. Gateway
origin acceptance uses the shared live reader separately.
