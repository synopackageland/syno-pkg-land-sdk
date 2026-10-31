# Untrusted HTTP destinations

Use `outbound.NewTransport(responseHeaderTimeout)` as an HTTP client's transport
when a URL, image registry or token realm comes from an untrusted input. Set the
client timeout or request context to bound the whole transfer. `ValidateURL`
checks syntax and literal addresses; it does not replace the transport's DNS
checks.

The transport accepts HTTP and HTTPS public destinations, disables environment
proxies, rejects loopback, private, link-local, shared-address, metadata,
multicast, reserved and IPv4 translation/tunnel destinations, and checks every
DNS answer. A mixed public/private answer is refused. Each new connection dials
an already checked IP without a second lookup; TLS authenticates the original
hostname. Every HTTP request, including each followed redirect, is validated.

The delegated URL download worker, Compose image resolution (including registry
authentication realms), and registry diagnosis use this policy by default.
Downloads and diagnosis refuse redirects. Image resolution permits at most five
redirects and strips authorization when the authority changes. A private registry
or LAN download is therefore refused by these defaults.

A trusted application's explicitly supplied HTTP client/transport controls its
own destination policy. Replacing the protected transport opts out of these
checks. This library does not restrict the application's other network clients
or Container Manager's own image pulls.

# Request admission and producer ownership

The executor bounds parsing/authentication to 64 connections with a 15-second
initial deadline. After authentication, at most 16 operations run concurrently,
with at most four per DSM account (name and UID), shared across sessions, batches
and protected offline grants. Capacity refusals use the retryable
`EXECUTOR_PAUSED` response before work is admitted. Invalid or unbound requests
share a separate four-request identity; the outer connection bound also applies.
This bounds cooperative callers and does not promise resistance to a compromised
package process flooding the pre-authentication pool.

Gateway pre-authentication transactions use a separate mutex from live sessions.
A client address can hold at most 64 transactions and 64 sessions; an account can
hold at most 64 sessions. A full client or global pool refuses new admission with
HTTP 429 instead of evicting another client's live state. Client limits use the
observed request address, including authenticated native ingress, never a browser
ID or an untrusted forwarding header. Clients sharing a NAT share its limit.

HTTP/bridge `delegate.move_from_staging` accepts sources only below
`delegate.OwnerStagingPrefix(caller.User(), delegationID)`. This is
`owners/<sha256(username + NUL + delegationID)>/`, with lower-case hexadecimal.
Grant ownership is still checked against the verified caller and current UID.
HTTP/bridge admission also requires the job ID to end in
`delegate.OwnerJobSuffix(user, delegationID)`: a hyphen plus the first 32 hex
characters of that owner hash. Keep the producer-local part at most 95 bytes.
For example, hash the producer job ID and append this suffix. Reusing a job ID with another
grant or source is refused by the durable admission binding. The trusted daemon's
`MoveFromStaging` facade remains a package-controlled producer API; its source
paths do not come directly from browser parameters.
