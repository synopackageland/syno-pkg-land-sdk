
## Provider-only OIDC

`mode: oidc` with `upstream_access: public` delegates App routes to upstream
authentication while `access` still protects DSM OIDC issuance. Native clients
need no gateway cookie or browser Origin. The issuer still requires DSM sign-in
and App Privileges; upstream must enforce its own authentication, CSRF and
WebSocket policy. See [the design and security boundaries](provider-only-oidc-design.md).
Use the matching SDK and CLI 0.27.0 releases for this field.

## Native OIDC browser navigation

`oidc.browser_redirect: {}` opts into per-request browser origins for SDK/CLI
0.24. Optional `html_paths` and `same_origin_referrer` configure HTML behavior.
No host list is accepted or stored in the SPK: users can open the same install
by IP, hostname, DDNS or a new DNS alias. Each request must use a syntactically
valid IP/DNS authority and the declared gateway scheme/port; forwarding headers
cannot select the destination. DSM native ingress uses its HTTPS port.
Host selects only this browser response's navigation URL, never an upstream
dial target or an authorization decision. DSM admission, transaction origin
and browser binding, exact redirect registration and PKCE remain required.
Gateway cookies are host-only. This does not make public upstream routes a
network security boundary; upstream retains its authentication/CSRF duties.
Use trusted DNS and TLS, as a user signing in on an attacker-controlled origin
exposes that origin's session to its owner. Rewritten responses use no-store
and remove validators/digests; issuer/discovery never depend on Host.

Location redirects and declared `rewrite_json` fields retain their existing
rewriting. Optional `html_paths: ["/auth/redirect"]` additionally rewrites only
exact upstream GET/200 HTML responses, including percent-encoded and escaped
authorization URLs in inline scripts. Only the fixed loopback `/authorize`
endpoint becomes this request's `/_sso/authorize`; query bytes are preserved.
HTML is bounded to 1 MiB, uses identity encoding and must not be a download.
Unsupported content fails explicitly. Changed responses are not cached and
stale validators/digests are removed. CSP is preserved; consumers using hashes
of rewritten inline scripts need an upstream-compatible policy.

For native forms whose upstream `no-referrer` policy makes browsers send
`Origin: null`, `same_origin_referrer: true` sets `Referrer-Policy: same-origin`
on only the declared HTML paths. Include the form page as an exact `html_paths`
entry. The default preserves the upstream policy. Referrers still never leave
the origin; null/foreign Origin requests remain rejected by the gateway.

Discovery, issuer and ID token `iss` stay fixed on loopback, as do token,
userinfo and JWKS. Server-side discovery caches remain valid. Redirect allowlists,
PKCE, client authentication and browser-bound DSM transactions are unchanged.
This requires matching SDK/CLI 0.24 support.

Server-managed OIDC consumers that already redirect to their provider should
leave `browser` unset. `oidc.native_login: true` opts into #37's native DSM cold
sign-in/resume independently of HTML/SPA script injection. The matching CLI
retains bounded launch parameters; the SDK classless Compose entry also accepts
only a typed JSON `launchParam` containing `ssoResume` (with the former query
form accepted for compatibility). The value is a transaction and declared
public port. DSM URL launchers may serialize a string as contiguous character indexes;
the entry normalizes exactly one bounded ASCII layer before reconstructing the fixed
same-origin authorize CGI and gateway return path, never a caller-supplied URL.
A second absent DSM session still fails visibly rather than redirecting again.
Normal entry without a resume parameter is unchanged.

The gateway overwrites `X-Forwarded-Host`, `X-Forwarded-Proto` and
`X-Forwarded-Port` from the validated public request authority (default 80/443).
Applications can generate their native OIDC callback/logout URLs on a dedicated
nonstandard gateway port without trusting a client-provided forwarding header.

`OIDCConfig.Browser` opts into native SSO entry with fixed `start`, exact
`login_paths`, `callback_paths` and an optional `completion_query` presence
marker. The browser script observes History API transitions, invokes the native
SSO start, and restores only a bounded local destination. It never reads/writes
upstream tokens, credentials or localStorage. Upstream callbacks own their own
protocol. A callback marker value is never read. Native non-HTML responses and
downloads are unchanged; HTML must permit the same-origin script through CSP.
The gateway does not weaken CSP. Oversized/compressed/unrecognized HTML fails
explicitly. Tab sessionStorage is required for automatic navigation; otherwise
the native login remains available. Pending state expires after two minutes.

With the corresponding CLI wrapper, `AuthorizeCGI.LoginApp` resumes a cold DSM
login through a fixed App launch. The launch parameter carries only the bounded
transaction ID and a declared gateway port. The generated window constructs the
same-origin authorize CGI URL; caller-provided URLs are never navigated. A second
missing-session response fails visibly instead of repeating the login redirect.
Gateway transactions expire in five minutes and remain browser/origin-bound.

A classless url launcher resumes the same way without a window class: DSM
forwards the launch parameter to `sso/open.cgi`, which accepts only one
`ssoResume=<transaction>:<declared port>` value and redirects to the
same-origin authorize CGI. This applies when `oidc.native_login` is set or an
`oidc.browser` bridge is configured; any other resume request is refused.

## Fixed menu destination

`launch_path: /#/sso?identifier=fixed-organization` selects a fixed local App
URL when the DSM menu opens the classless gateway entry. Paths, queries and SPA
fragments are supported. The default remains `/`. Use SDK and CLI 0.27.0 for matching launcher and runtime support.

The target is trusted package configuration, never a caller-supplied return URL.
The launcher retains the DSM hostname and declared public gateway port. It rejects
absolute/network URLs, backslashes, control characters, dot segments and reserved
`/_sso` paths. Existing sign-in transaction resume continues to use the issuer
root and the exact callback, independently of this destination. OIDC client,
PKCE, App Privileges and upstream authentication are unchanged.

Use an upstream's documented native entry route. This does not inject scripts,
patch upstream UI, create an App session or unlock encrypted data. App-specific
query/fragment behavior and any App open-redirect parameters remain the App's
contract and must be tested. Ingress without native TLS still shows the existing
direct-access notice with the fixed destination as its continue link.
