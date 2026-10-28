# Gateway access failures

An explicit App `entry` is the admission floor for proxied traffic, including
OIDC mode, uploads and WebSocket upgrades. Fixed `upstream_auth_paths` delegate
only the listed upstream prefixes to the App's own authentication. Public
gateway/upstream access cannot combine with explicit entry. See
[App entry enforcement](app-entry-access.md) for the access-path inventory.

## Upstream-authenticated paths

Declare prefixes in the package manifest:

```yaml
entry: admin-only
identity_gateway:
  # Keep the App's existing sdk, mode, upstream, listen and adapter fields.
  upstream_auth_paths: [/rest, /share]
  passthrough: [/rest, /share]
```

The corresponding gateway JSON field is `upstream_auth_paths`; direct Go
configurations use `Config.UpstreamAuthPaths` or `FrontConfig.UpstreamAuthPaths`.
The field accepts 1-16 nonoverlapping, case-sensitive absolute prefixes of at most
512 characters. Use URL unreserved characters and `/`. Root, empty values, dot
segments, repeated slashes, percent encodings, backslashes, query/fragment
delimiters and wildcards are rejected. `/_sso` and `/_spl` are reserved. `/rest`
and `/rest/` both match `/rest` and descendants, never `/restore` or `/REST`.
The gateway decodes requests once before matching and rejects ambiguous dot
segments, encoded separators, nested delimiter escapes and repeated slashes.

Each listed prefix directly proxies to the upstream without DSM admission,
gateway identity injection, login redirects or response rewriting. The proxy
preserves upstream credentials, request bodies, queries, conditional headers and
upstream responses. It removes DSM cookies, gateway cookies, SDK `spl_session_*`
cookies, and configured or reserved identity headers, including client-supplied
spellings. Normal reverse-proxy forwarding headers and hop-by-hop handling still
apply. Readiness, upload limits and blocked Shared Folder activation still apply.

Permanent and optional passthrough must be fully contained in the declared
prefixes when this field is present. Optional passthrough remains default-off and
retains its enable/disable checks and stream cancellation. With explicit entry,
passthrough without this declaration is rejected. All other paths retain DSM
entry admission, including WebSocket and upload paths. The OIDC issuer and SDK
RPC methods keep their independent entry policy; an upstream prefix cannot
exempt a method from `permissions.json` or the SDK registry.

**Security:** these paths are only as strong as the upstream's own authentication,
authorization, CSRF and WebSocket policy. The declaration also covers browser
requests to these paths. Upstream-public share links remain public; DSM logout
does not revoke upstream tokens, sessions or active connections.

The CLI requires SDK sources containing `UpstreamAuthPathsVersion = 1` and links
the static entry policy digest into the gateway. Editing the entry mode,
upstream-auth paths or passthrough settings without rebuilding refuses startup
and CGI loading. Use matching SDK/CLI sources until an approved release ships
this capability.

The identity gateway distinguishes DSM authentication from App admission. A
verified DSM session does not grant administrator or admitted-group membership.
The authorize CGI returns `access_denied` for admission refusals, including DSM
App privilege denial, and the
gateway renders a policy-specific explanation without a retry link or automatic
redirect. It checks membership again when redeeming the assertion and on each
protected App request, so demotion takes effect on the next request.

| HTTP | Gateway error code | Message key | Recovery |
| --- | --- | --- | --- |
| 403 | `dsm_admin_required` | `gateway.admin_required` | Use an account admitted as a DSM administrator |
| 403 | `dsm_group_required` | `gateway.group_required` | Ask the DSM administrator about App access |
| 403 | `dsm_app_privilege_denied` | `gateway.app_privilege_denied` | Ask the DSM administrator to grant this App's privilege |
| 401 | `dsm_sign_in_required` | `gateway.sign_in_required` | Sign in to DSM at this address, then retry |
| 503 | `dsm_verification_failed` | `gateway.verification_failed` | Retry when DSM verification is available |

A failed membership lookup is a verification failure, not evidence that the
account lacks permission. Backend error details are never returned to browsers.
The CGI carries App privilege refusal as `error=access_denied` with the fixed
`error_reason=app_privilege_denied`, so it is not mistaken for administrator or
group policy. This applies to OIDC, header, token and gateway-only modes. Silent
OIDC still returns the standard `access_denied` protocol error.

For navigations, a missing gateway session first redirects to the DSM authorize
CGI. Its result page always uses HTML. Later navigation failures use the same
HTML page; protected API requests instead receive JSON:

```json
{"error":"dsm_admin_required","message_key":"gateway.admin_required","message":"This App requires a DSM administrator."}
```

HTML exposes `data-error-code` and `data-i18n` attributes. Codes and keys are
stable; applications should not parse translated text. The gateway embeds its
own catalogs because it operates before the App frontend loads. It selects a
locale from `Accept-Language`, defaults to English, and supports the SDK's nine
locales: `en`, `de`, `es`, `fr`, `it`, `ja`, `ko`, `zh-CN`, `zh-TW`.

Silent OIDC (`prompt=none`) keeps protocol redirects with the original state:
`access_denied`, `login_required`, `temporarily_unavailable`, or `server_error`.
Interactive denial displays the gateway explanation. No refusal issues a new
gateway session or OIDC code, or reaches the upstream App/token adapter.

Gateway session expiry still triggers DSM verification. This does not change
the gateway's existing DSM-session lifetime or revoke an App's independent
native sessions on explicitly configured passthrough routes.


## Optional native-client access (next minor SDK)

`optional_passthrough` declares fixed App-authenticated prefixes, separately from
permanent `passthrough`. For example, the CLI manifest can declare:

```yaml
identity_gateway:
  # Other gateway fields omitted. This example uses SDK 0.27.0.
  sdk: 0.27.0
  optional_passthrough:
    mobile:
      prefixes: [/dav, /api/mobile]
```

Every entry defaults off; there is no manifest switch to enable it. Prefixes
match whole segments, cannot include the root or `/_sso`, and cannot overlap
other optional or permanent passthrough prefixes. The gateway rejects ambiguous
paths (dot segments, encoded separators, nested delimiter escapes, repeated
slashes and backslashes). WebDAV `Destination` must stay on the same origin and
inside the same declared entry. Ordinary spaces, Unicode and literal percent
file names are supported. Other paths retain the existing DSM admission policy.

Apps register `cfg.OptionalPassthroughRoutes()` on their existing SDK API/CGI,
where `cfg` is the same loaded gateway `Config`. Both
`gateway.passthrough.status` and `gateway.passthrough.set` are immutable
administrator-only SDK methods. `status` takes no parameters; `set` takes only
`{"id":"mobile","enabled":true}`. Do not expose an unprotected alternate control
endpoint. Show the switch only to administrators. Before enabling it, explicitly
warn that these paths bypass DSM sign-in, rely solely on the App's own account
credentials, and let external clients read/write files authorized by that App.
The App account also needs its native WebDAV/API permission. Users can disable
access at any time. Do not inject a shared credential or claim DSM logout revokes
the App's native credentials.

The SDK atomically replaces a private 0600 `passthrough-<id>.json` boolean in the
existing 0700 `state_dir`, then sends SIGHUP to `gateway.pid`, checking that the
Linux process has the same nonroot UID. No new port, root helper, socket or
process restart is needed. Each reload replaces a context and cancels active
optional requests (including streams); switching any entry interrupts all
optional requests. Permanent passthrough is unaffected. State survives gateway
restarts; uninstall retention follows the package's existing data policy.

Closing blocks the next optional request with 403 **before DSM authentication**,
even for an administrator session and before SIGHUP is handled: admission also
checks the private state file. Missing, malformed, symlink or nonprivate state
files fail closed. Opening requires a successful reload. A failed signal returns
an error and writes disabled state. Responses report `configured_enabled` and
`reload_requested`, not a reload acknowledgement. After enabling, a client may
briefly receive 403 until the signal is handled. Changes to the fixed manifest
prefixes require rebuilding/restarting the gateway; changing the boolean does not.

This is unreleased next-minor functionality. No package version is bumped by
this change; Apps must wait for an approved SDK/CLI release before adopting it.
