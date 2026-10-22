# Provider-only OIDC

Status: development implementation; release and device acceptance remain separate.

## Declaration

Use `mode: oidc`, `upstream_access: public`, and the existing `access: dsm-users`,
`dsm-admins` or `dsm-groups` (with its group allowlist). Keep the normal upstream,
listen and OIDC client declarations. Omitted `upstream_access` means `protected`
and preserves existing packages. Public upstream access requires explicit DSM
issuer access; it never turns the issuer public.

All non-reserved upstream routes delegate authentication to the App. This adds
no per-path feature, toggle, forward-auth, shared service or App-specific adapter.
Optional passthrough and blocked Share activation cannot combine with it: a
global public policy must not bypass a closed optional feature or unapproved mount.
Existing permanent prefixes are redundant and retain their semantics.

## Security boundaries

| Boundary | Treatment and reason | Limits |
| --- | --- | --- |
| Upstream DSM admission | Deliberately bypassed. Native APIs/token POSTs and WebSockets authenticate themselves; no SDK session, DSM redirect or trusted subject is attached. | Apps must enforce authentication and authorization. Their own public routes remain public. CLI explicitly reports this exposure. |
| Upstream Origin/Referer | Deliberately bypassed, including foreign Origin and native WebSockets without Origin. The App owns browser/native-client policy. | App must enforce CSRF, CORS and WebSocket authorization. The gateway adds no permissive CORS headers. |
| `/_sso` and `/_sso/*` | Exclusively owned by the gateway and dispatched before public proxying. Existing origin/authority/method checks remain; unknown reserved paths return 404. | Token/userinfo/JWKS/discovery remain on the existing loopback issuer. |
| DSM sign-in | Existing DSM-origin authorize CGI verifies DSM cookie, SynoToken and client address. No assertion for no-session or directory failure. | Signing material, DSM CGI/runtime and the package account remain trusted. |
| App Privileges | Production CGI always uses EntryGate. For this feature an omitted entry resolves to `app-privilege`; explicit `admin-only` remains stricter. Ordinary users need `SYNO.SDS.<package>.Instance` permission for their client address. | Existing semantics admit administrators directly. Existing DSM privilege decision cache is at most 30 seconds; failures deny. |
| Issuer access | AccessVerifier checks dsm-users/admins/groups before signing; role/group admission is checked again at assertion redemption. Public/empty issuer access is invalid. | An upstream public request never invokes grant issuance. |
| Authorization grant | Existing registered client, exact redirect allowlist, state, PKCE, signed assertion, transaction/browser/origin binding and expiry remain. | Transactions last 5 minutes, assertions 1 minute. A rejected identity creates no code. |
| Token issuance | Client authentication, nonexpired single-use code, exact redirect and PKCE remain required. No password or client-credentials grant is added. | Token POST needs no DSM cookie: its code comes from the verified DSM/App authorization. Pending codes expire after one minute; there is no new live DSM recheck at redemption. |
| Cookies/headers | Native App cookies and bearer tokens survive. Reserved DSM/all SDK cookies and spoofed identity/DSM-token headers are filtered. Public routes never inject a subject. | Upstream credentials belong to the App; gateway login does not create an App session by itself. |
| Readiness/transport | The existing bounded proxy retains lifecycle/health readiness, upload limits and transport checks. Unsupported upgrade protocols remain rejected. | Public access cannot activate an unapproved Share or revive a released Compose generation. |
| Revocation/account deletion | Existing watcher and RevokeUID behavior remain. | App sessions, API keys and offline encrypted vaults are outside gateway admission; do not promise immediate revocation. Account-bridge/upstream contracts must define this. |
| Host/network security | Existing package UID, native ingress and upstream endpoint constraints remain. | Host code execution, DNS ownership, malicious upstream, reverse-proxy/TLS configuration and automatic upstream provisioning are outside this change. |

## Compatibility and inspection

Config loading and NewFront validate the same policy. Existing public none mode
and protected modes retain their behavior. Old SDKs reject the unknown field.
CLI build checks the actual SDK source capability marker, so a development
override with an old release pin does not imply that release shipped the feature.

CLI embeds an admission summary in the build record and compares it with the
packaged config during inspect. It reports `upstreamProtected: false` and
**Upstream routes are not protected by the gateway**, alongside issuer access.
This explicit supported exposure is informational. Missing/inconsistent metadata,
invalid public issuer admission or Share bypass is an inspection finding.

## Validation

Tests are red against the previous unknown-field rejection, then green with this
implementation. New scenarios cover native/no-Origin and foreign-Origin traffic,
credential preservation/DSM filtering, readiness, missing DSM session, denied App
privilege, role denial, verifier failure, reserved-route Origin enforcement,
nonexistent public token endpoint, successful verified PKCE exchange, replay,
invalid declarations and the production CGI App Privileges chain. Existing
idgateway tests cover redirect/browser binding, expiry, streams and sessions.
Fixture tests do not establish real DSM privilege-bridge behavior; device evidence
and any development source overrides must be reported separately.
