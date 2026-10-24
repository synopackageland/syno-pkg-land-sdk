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
A [single-origin App](external-access.md#single-origin-apps) also accepts an
explicit port in `manual_origin`, such as its tailnet address on the App port
(`https://<name>.ts.net:<App port>`, the private URL NAS Remote Connect shows).
The App origin cannot equal the effective DSM origin or the DSM address the
page is open on, port included.

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

With SDK 0.30.1, an entry may also name exactly one installation setting as a
fallback:

```yaml
DOMAIN: {external_origin: true, setting: canonical_origin}
```

Declare `canonical_origin` in `install.settings` with an origin type such as
`https-origin`. The host uses that value only when the effective App origin is
empty. Without `setting`, no installation setting is required. `secret` and
`oidc_client` cannot be combined with `external_origin`, with or without a
fallback. The service still needs normal nonroot storage initialization and
explicit argv.

At package preparation, the host uses the shared `EffectiveExternal` resolver:
manual, then Connect, then the App's configured `public_origin`. If that result
is empty and a fallback is declared, it reads the installation value on the
host. A nonempty fallback must pass the same bounded, private, package-owned,
single-link file and HTTPS-origin checks as the snapshot; unsafe files and
invalid URLs fail preparation. An absent optional value or an empty private
value contributes no origin. An external origin takes precedence without
reading an unused fallback. The host atomically writes a mode-0600 snapshot
at `target/compose/settings/external-origin.value`.
The planner mounts only that file, read-only, at
`/synopkgland/settings/external-origin.value`. Containers receive neither gateway
state, the installation value file, nor the Connect projection. Multiple
environment keys share one mount. All external-origin entries across services
must name the same fallback, or all omit it; conflicting policies are rejected
by both the planner and installed preflight. The inventory retains only the
setting reference, never its private value.

With neither an effective origin nor a fallback value, the snapshot is empty
and the runtime leaves the environment key unset, including removal of an inherited image value. An
origin is not a required installation setting. The empty file keeps the bind
source valid without Docker creating a directory in its place.

The snapshot is refreshed in the installed host preflight before the worker
starts containers, as well as during installation preparation. Restart the
App after changing its address to update its container environment. Clearing
the external origin restores the installation value on the next start; changes
to that value are also rechecked. Gateway
origin acceptance uses the shared live reader separately.

## Vaultwarden and alternate LAN addresses

Vaultwarden 1.37.4 with `SSO_ONLY=true` needs a canonical URL for browser SSO.
When `DOMAIN` names the public host, starting SSO on a different LAN hostname
or IP does not preserve that sign-in flow. The gateway rejects the callback's
different origin. Allowing that callback alone still leaves an upstream
constraint: the callback uses `DOMAIN`, while the browser-binding cookie
belongs to the host that started authorization. Vaultwarden rejects the
missing cookie with `SSO session binding mismatch`.

Use the same canonical URL for internal and external SSO, with reachability
from both networks arranged by the deployment. A tailnet address on the App
port, `https://<name>.ts.net:<App port>`, works from every device on the
tailnet, including LAN devices that join it; set the same value, port included,
as `DOMAIN`, which accepts a port. Declare
[`single_origin: true`](external-access.md#single-origin-apps) so the DSM
launcher and other hosts lead browsers to that URL. A working `external_origin`
snapshot and an acknowledged origin-change warning do not establish alternate
LAN-host SSO support.

See the upstream [DOMAIN configuration](https://github.com/dani-garcia/vaultwarden/blob/7c188967e7a467b19b2757bc3326f9cf3ccae327/.env.template#L180-L190)
and [callback cookie check](https://github.com/dani-garcia/vaultwarden/blob/7c188967e7a467b19b2757bc3326f9cf3ccae327/src/api/identity.rs#L1134-L1141).
