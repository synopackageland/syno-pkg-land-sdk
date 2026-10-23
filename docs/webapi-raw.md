# Raw WebAPI responses

`webapi.call` remains JSON by default. A named administrator-approved profile
opts into raw responses with one optional `raw_max_bytes` integer (1..67108864).
Omitting it or setting zero keeps JSON-only behavior and the old profile digest.
Studio Office should use 26214400 (25 MiB). Changing the limit changes the policy
and profile digests: administrator approval and fresh user consent are required.

The request adds only `response_type: "raw"` (or `"json"`, the default). There is
no request-selected filesystem path, inline threshold, encoding or header map.
The profile owns the body limit; an extra caller limit is unnecessary for the
current consumer. Future per-call limits can narrow that ceiling additively.

Raw success returns `response_type: "raw"`, `content_type`, `size`, `sha256`, and
exactly one of `body` (base64, including empty string) or `staging` (bare handle).
At most 32768 body bytes are inline, keeping the existing 48 KiB result / 64 KiB
frame bounds. Larger bodies use the existing executor-staging transport:
`/var/packages/<package>/var/executor-staging/root-<32 lowercase hex digits>`.
The directory is private (0700), the random file is owned by the App (0600).
Read, verify size and SHA-256, and immediately unlink it. Never send the path,
handle or unfiltered MIME to a browser. Go `JobContext.WebAPI().CallRaw` consumes
both forms and removes staged output, including on verification failure. Bridge
Apps use their existing `webapi.call` service and consume the same wire result
locally. They must not inline a large body into a bridge/RPC reply.

This reuses the root output spool rather than adding a download endpoint, token
store or new privileged operation. The existing package quota (256 MiB), two
output slots and one-hour orphan reclamation bound abandoned output. An App may
only consume its own spool; it never gets access to root state or another App.
The process runs as the consenting user's DSM runner; the executor strips CGI
headers and writes only body bytes to the App spool. This does not expand the
API/method/version allowlist or allow File Station through `webapi.call`.

CGI parsing accepts CRLF or LF headers, at most 8192 bytes and 32 fields. It
requires a header/body separator, valid header names/values, singleton headers
without duplicates, a 2xx Status if present, no transfer/content encoding, and an
exact Content-Length if supplied. Content-Type is at most 256 bytes; absent it
defaults to application/octet-stream. Arbitrary headers (including Set-Cookie)
are discarded. Location and X-Accel-Redirect are refused, never followed or
interpreted as local paths. MailPlus Attachment.download is outside this feature.

The 10-second execution/revalidation deadline remains. The subprocess streams
stdout; invalid headers or a body over the profile limit terminate the process
group and remove partial staging. `limit-exceeded` explicitly means no complete
body was returned. Execution/invalid reply/audit failures remain nonretryable
`WEBAPI_UNCERTAIN`; staging failures return `STORAGE_ERROR`. No response is
truncated and labeled successful. Audit records mode, byte count and result,
never CGI headers, MIME, subject, attachment bytes or params.

Example profile and background Go call:

```json
{"purpose":"Read original messages to create tickets","raw_max_bytes":26214400,
 "apis":{"SYNO.MailClient.Message":{"get_original":[10]}}}
```

```go
job := delegate.JobContext{Client: delegate.Local(packageID),
    DelegationID: grant.ID,
    StagingDir: "/var/packages/" + packageID + "/var/executor-staging"}
original, err := job.WebAPI().CallRaw(ctx,
    "SYNO.MailClient.Message", "get_original", 10, map[string]any{"id": messageID})
// original.Body is the exact MIME; original.ContentType, Size, SHA256 describe it.
// Persist/parse locally. Do not automatically retry uncertain mutations.
```

MailPlus v10 adapter: DSM `synowebapi --exec` appends a separate error-117
JSON reply after its CGI MIME stream. Those bytes are not part of the original
message. For exactly `SYNO.MailClient.Message.get_original` v10, the SDK uses
`outfile=/proc/self/fd/3` with an inherited pipe: the handler writes the original
MIME there, while stdout carries only the bounded JSON control reply. The known
117 control reply is accepted only after nonempty body output; no MIME suffix
is guessed or stripped. This adapter declares `message/rfc822`. Other raw calls
retain the bounded CGI stdout path and need their own compatibility probe before
shipping. Caller-supplied `outfile` is permanently refused in both modes.

Use SDK/App/CLI 0.27.1 with templates 0.27.x and its installed UI pin.
Upgrade backend/frontend exact pins together and rebuild the App. A changed
profile policy requires administrator approval and fresh user grants. The
optional profile is declared by the consuming App; no template overlay is needed.
