# Gateway access failures

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
  # Other gateway fields omitted. This capability requires SDK 0.24.0.
  sdk: 0.24.0
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
