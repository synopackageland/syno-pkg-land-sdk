# Standalone identity

Enable only with `-X main.identity=standalone`. The CGI creates this service from
its package var directory and trusted `SCRIPT_NAME`; `internal/app/identity.go`
registers the transport. The Vue shell discovers `identity.status` at runtime.
The CLI worker must wire `capabilities.identity: standalone` to the ldflag.

The transport accepts JSON POST requests that pass the App's origin guard
(`api.CheckOrigin`, `api/REQUESTS.md`), which it applies before any identity
or business step, transfers included:

- `identity.status {}` reports whether this transport is enabled.
- `identity.session {bootstrap: true}` exchanges the incoming DSM `id` cookie
  for an app session when no app cookie is present. Without bootstrap it reads
  the app session. Expired app cookies are cleared before another bootstrap.
- `identity.login {account, password, otp}` verifies credentials with DSM Auth
  v7, without a `session=DSM` argument. `otp_required` requests the second step.
- `identity.logout {}` removes the app session and clears the cookie.

DSM's native `authenticate.cgi` checks the incoming cookie, SynoToken and
original browser address using the same authenticator as the package CGI.
Anonymous Initdata success cannot establish identity. Cookie bootstrap never
replays an existing browser session to loopback HTTP: DSM's IP binding can
invalidate that session (`skip_ip_checking=no`). DSM web logins require their
SynoToken; cookie-only sessions remain subject to DSM's own policy. The token
travels only in the native child's explicit CGI environment (header and encoded
query), never in HTTP URLs, argv, logs or storage. Local `user.Lookup` binds the
verified username to its UID.
Auth login creates a temporary SID, then immediately logs it out before issuing
an app session. A logout failure refuses the app login. No password, OTP, DSM SID
or DSM cookie is logged or persisted. Only hashes of app bearer IDs are stored.

The package-private store uses directory mode 0700, files mode 0600, atomic
replacement and a cross-process lock, so CGI restarts preserve sessions and
rate limits. Sessions have a default 15-minute expiry. Sliding requires a live
DSM cookie; password-only sessions cannot extend themselves indefinitely.
Login limits are five attempts per account and twenty per IP per minute;
forwarded client address and HTTPS headers are not trusted.

Cookies are host-only, HttpOnly, Secure and SameSite=Lax, scoped to the app path,
with a package-specific name. Lax supports top-level app navigation. A request
that another site or port sent answers `cross_origin`, one that proves no origin
`origin_required`.
Password login and cookie bootstrap require HTTPS, determined by Go's TLS state
(the CGI library maps the trusted `HTTPS` environment variable to that state).

The package account has no verified disabled-account reader. Every operation
except the explicit read-only `app.status` and `greeting.get` therefore requires
both a matching local UID and live DSM cookie verification. Missing DSM cookies
produce `session_revalidation_required`; invalid cookies revoke the app session.
Password-only login supports those basic reads for at most 15 minutes. Different
hostnames need a same-host DSM login before sensitive operations can succeed.
App business permissions live in `internal/api/permissions.json`; SDK-owned
routes use `sdk-permissions.json` in package `api` of the SDK, verified against the CLI policy.
app identity grants no File Station authority and does not create a NAS session.

New business methods default to live revalidation. Do not add a read exception
for sensitive data or state-changing operations. Unavailable account checks fail
closed. Tests use a fake DSM server; actual DSM/Web Station deployment remains
an integration acceptance step.

File metadata and transfers return `dsm_session_required` for an App session
without a DSM cookie. Sign in to DSM on the same hostname to use File Station.
HTTP streaming transfers pass through the central API policy and identity
revalidation without JSON buffering.
