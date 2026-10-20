
## Native OIDC browser navigation

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
