# Private media in native audio and video elements

Use the Go `media` service and TypeScript `mediaUrl` helper for an App's private
recordings or videos. The URL stays on the App's existing CGI or daemon ingress;
there is no public endpoint or global service. The App runs as its package
account and opens only assets its owner hook authorizes.

A native `<audio>` or `<video>` request includes cookies but cannot set
`X-SYNO-TOKEN`. A normal authenticated `fetch` can set that header, but a Blob URL
made from its result can still fail: DSM shell CSP can disallow `blob:` as a
media source. Codec support does not override CSP. A same-origin grant URL lets
the browser stream and seek directly, without changing CSP, a Service Worker,
PCM windows, buffering the complete file, or placing a DSM token in the URL.
The App still needs a browser-supported media format.

## Go wiring

```go
fresh, err := dsm.NewMediaAuthenticator(entryMode, packageID)
if err != nil { return err }
playback, err := media.New(media.Config{
    DataDir: packageDataDir,
    Endpoint: apiEndpoint, // same-origin path to CGI or daemon ingress
    Entry: "SYNO.SDS." + packageID + ".Instance",
    FreshAuth: fresh,
    Generation: currentInstallGeneration,
    Authorize: authorizeAsset,
    Open: openAsset,
})
if err != nil { return err }
server, err := api.NewServerWithPermissions(
    playback.Authenticator(existingAuthenticator),
    permissionsJSON,
    append(existingRoutes, playback.Routes()...)...,
)
```

Import `api`, `dsm`, and `media` from the SDK Go module. Check every constructor
error. `existingAuthenticator` keeps the App's regular RPC admission and entry
floor. Use the official fresh authenticator for media: wrapping a session or
entry cache here would delay logout and privilege revocation. A global middleware
that requires a token header before dispatch must let this service's fixed media
selector reach `api.Server`; it must not turn off authentication generally.

Declare these **App-owned** permissions and operation metadata alongside your
other operations (and regenerate the App's typed contracts):

```json
{
  "media.issue": "authenticated",
  "media.renew": "authenticated",
  "media.revoke": "authenticated",
  "media.content": "authenticated"
}
```

`media.content` is a stream operation; issue/renew/revoke are JSON operations
with write effects and must not use automatic read-RPC recovery/replay. The
constructors retain the central permissions policy and explicit entry mode.
No method uses `public` or `integration` permission. A custom `Prefix` changes
all four method names together; configure the browser with the same prefix.

The trusted hooks have these contracts:

- `Generation(ctx) (string, error)` reads the **current installation generation**
  on every request, including a still-running old daemon after an upgrade. Use
  an installation identity replaced by the package lifecycle on reinstall or
  upgrade. Do not use a constant, a user-supplied value, the display version, or
  a generation captured only at process startup. Missing/error results refuse.
- `Authorize(ctx, caller, asset) (revision, error)` verifies ownership/read access
  for the verified DSM caller. It runs at issue, renew, and every transfer.
  Return a stable owner/authorization revision; changing it invalidates the old
  grant. Asset IDs are opaque App IDs, not arbitrary filesystem paths or URLs.
  Administrators do not bypass this hook.
- `Open(ctx, caller, asset) (media.Asset, error)` resolves and opens that same
  authorized asset, returning a seekable, closable `Content`, an explicit
  `audio/*` or `video/*` `ContentType`, and a **quoted strong ETag** for the exact
  bytes. For example an immutable recording digest can be the ETag. Do not use
  the grant ID as the ETag. The SDK always closes Content. Pin the authorized
  asset revision when opening so concurrent asset replacement cannot substitute
  another person's file after the hook runs.

Native DSM account lookup binds the grant to username **and UID**, and refuses
unknown, disabled or expired accounts. `LookupUID` is an advanced trusted hook;
keep the same native enabled-account checks if replacing it for another host.
No browser-supplied owner or UID is accepted.

## Browser wiring

```ts
import {mediaUrl} from '@synopackageland/sdk'

const media = await mediaUrl(ctx, {
  asset: recordingID,
  onError: error => showPlaybackRenewalError(error),
})
audio.src = media.url
// Native controls, seeking, and pitch-preserving playbackRate remain available.
// On unmount:
media.dispose()
audio.pause()
audio.removeAttribute('src')
audio.load()
```

`mediaUrl` calls `media.issue` using the ordinary cookie + token RPC transport,
then renews with authenticated `media.renew` halfway through the returned TTL.
Renewal returns the **same URL** and refreshes its sealed cookie; it does not
replace `src`, call `load()`, or move `currentTime`. Concurrent explicit
`media.renew()` calls share the same in-flight request. Dispose stops renewal
and aborts pending work. An optional AbortSignal also disposes the helper.

A renewal failure is exposed through `media.error` and `onError`. It stops the
renewal loop; there is no silent retry of media, mutations, or expired sessions.
After a transient error, `await media.renew()` explicitly retries while the grant
is still valid. After expiry or logout, obtain a **new** `mediaUrl` after normal
sign-in and owner checks; then the App can restore the intended time if desired.
`await media.revoke()` stops renewal and revokes through the authenticated RPC.
An App can also call Go `playback.Revoke(id)` when deleting/restricting an asset.
Revoking all assets of an owner can be implemented through the owner hook's
revision/access result without enumerating grant IDs.

## Security and lifetime

The URL contains only the fixed `method=media.content` selector and a 256-bit
random `media_grant`. It never includes a cookie, SID, SynoToken, asset path,
username, or UID. A grant is **not a bearer credential**: a request needs the
original DSM `id` cookie and a matching encrypted HttpOnly media cookie.
Even a different session of the same person cannot use the URL.

DSM's `authenticate.cgi` also requires the SynoToken. On issue/renew the SDK
seals that token into an AES-GCM authenticated, HttpOnly, SameSite=Strict cookie,
scoped to the exact endpoint path and named separately for each App/endpoint.
Only a media transfer can unseal it. It is bound to the DSM session hash, subject,
UID, App entry and installation generation. Tokens exist only in live request
memory and the encrypted browser cookie: the package-private disk store holds
only hashes, binding metadata, and a random 0600 encryption key. Back up the key
as sensitive material, and keep this directory accessible only to the package
account. No root helper is required. Do not log cookies, response Set-Cookie,
bodies or raw URLs. A grant cookie is unrelated to an offline delegation.

Every request runs uncached DSM session/entry validation, current UID/generation,
owner/revision and revocation checks before content opens. Logout, disabled
accounts, revoked App Privileges, owner changes, installation replacement,
expiry, or App revoke refuse the next request. An already-open response and bytes
already delivered to the browser cannot be recalled. Authorization is checked at
request admission; this is not a DRM or continuous in-flight revocation system.

The default inactivity TTL is **5 minutes**, configurable up to **30 minutes**.
These are credential validity windows, not elapsed-work deadlines. An admitted
GET/HEAD refreshes the grant and its sealed cookie, so continued Range activity
also keeps the URL usable. The browser renewer refreshes both while a loaded
element makes no new requests, without changing the URL. A suspended tab, network failure, or
blocked cookies can require a new grant; the SDK does not infer that a recording
failed just because renewal was delayed. At most 4096 live grants are stored per
service, with expired records removed on access. CGI processes share a locked,
atomic store; a concurrent renew cannot recreate a revoked grant.

HTTPS issues Secure cookies. HTTP remains usable and logs a warning when a
sealed cookie is issued; use HTTPS to protect both DSM and media cookies from
network observers. The same-origin checks and native DSM admission still apply
on HTTP. Do not forward a grant URL or cookies to a CDN or another origin.

Content responses use `Cache-Control: private, no-store`,
`Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, and
`Cross-Origin-Resource-Policy: same-origin`. SDK observers strip query strings
and credentials. Go `secret.Redact` and TS `redactSecrets` redact the exact
`media_grant` diagnostic field, without hiding ordinary `grant` or public
response values. Configure **reverse-proxy/access logs to omit query strings**
and credential cookies as well; the SDK cannot scrub DSM nginx or third-party
logs outside its process. Never send raw media URLs to analytics or error tools.

## Range and conditional requests

Only GET/HEAD are served. Other verbs carrying valid media credentials receive
405 and `Allow: GET, HEAD`; unauthenticated callers are refused by admission.
Go `http.ServeContent` handles byte ranges, multipart ranges, 206,
`Content-Range`, 416, HEAD, ETag, `If-None-Match`, and `If-Range`. Matching
If-Range returns the requested bytes; a stale validator returns the current
complete representation. Each seek/request re-enters the security checks.
No SDK body-size cap or short playback timeout is added. The existing CGI/ingress
request budget and DSM proxy limits still apply to an individual response.
