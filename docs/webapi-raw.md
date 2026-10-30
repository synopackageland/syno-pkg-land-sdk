# Raw WebAPI responses

`webapi.call` remains JSON by default. A named administrator-approved profile
opts into raw responses with one optional `raw_max_bytes` integer (1..2147483648,
at most 2 GiB).
Omitting it or setting zero keeps JSON-only behavior and the old profile digest.
Use a small limit for mail and 2147483648 for Studio Drive imports. Changing the
limit changes the policy and profile digests: administrator approval and fresh
user consent are required.

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
handle or unfiltered MIME to a browser. Go `JobContext.WebAPI().CallRawTo` consumes
both forms and removes staged output, including on verification failure. Bridge
Apps use their existing `webapi.call` service and consume the same wire result
locally. They must not inline a large body into a bridge/RPC reply.

This reuses the root output spool rather than adding a download endpoint, token
store or new privileged operation. Raw transfers reserve their approved ceiling
against a 2 GiB + 256 MiB staging budget, including already staged output and
active raw reservations. Admission is conservative if the disk scan already
counts another active file. The root.run quota stays 256 MiB. The two shared
output slots and one-hour orphan reclamation still bound abandoned output. An
App may only consume its own spool; it never gets access to root state or another App.
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

Raw execution allows 30 minutes, capped by the parent deadline and a timed
grant's remaining lifetime. The socket handler allows 30 minutes plus 10 seconds;
the default Go client waits 30 minutes plus 20 seconds. Explicit shorter client
timeouts remain effective. JSON WebAPI keeps its 10-second bound, and ordinary
DSM commands keep their existing budgets. A short CGI/bridge deadline still
wins: run large transfers in an existing background job.

Grant validity is checked at admission and completion. Revocation and integration
disable persist the existing fence, cancel active raw command contexts, and drain
admitted calls before acknowledging. Grant expiry cancels a timed transfer.
The exact API/method/version pin, account, UID and App Privileges checks remain.
All original-file reads run as the consenting DSM user; the package account only
reads the downloaded copy.

The subprocess streams
stdout; invalid headers or a body over the profile limit terminate the process
group and remove partial staging. `limit-exceeded` explicitly means no complete
body was returned. Execution/invalid reply/audit failures remain nonretryable
`WEBAPI_UNCERTAIN`; staging/quota failures return `STORAGE_ERROR`. Grant refusal
returns `DELEGATION_DENIED`, and integration disable returns `disabled`. No
response is truncated and labeled successful. Audit records mode, byte count and result,
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
tmp, err := os.CreateTemp(destinationDir, ".drive-import-*")
if err != nil {
    return err
}
defer os.Remove(tmp.Name())
defer tmp.Close()
result, err := job.WebAPI().CallRawTo(ctx,
    approvedAPI, approvedMethod, approvedVersion, downloadParams, tmp)
if err != nil {
    return err
}
if err := tmp.Sync(); err != nil {
    return err
}
if err := tmp.Close(); err != nil {
    return err
}
// Apply Studio's existing destination authorization/conflict/publication
// contract here. result.Size/SHA256 describe the verified local copy.
```

`CallRawTo` replaces the buffered `CallRaw` API. `RawWebAPIResult` now contains
only `ContentType`, `Size` and `SHA256`; there is no `Body`. Both the executor
and client use bounded memory. The client copies with a fixed 32 KiB buffer,
verifies size/hash, and unlinks staging on success or failure. It refuses
symlinks and checks inode identity, ownership, permissions and link count.
Writer errors survive unchanged and the SDK never closes the writer.

The writer may contain partial or unverified bytes after any error, including a
hash mismatch. The caller must remove its temporary destination and publish only
after success. Cancellation is checked between reads/writes; a blocking custom
writer must implement its own cancellation. Small consumers such as MIME parsing
can explicitly supply a `bytes.Buffer` under their existing small profile limit.
Large consumers must not buffer or encode the whole body into bridge/RPC output.
A destination copy can temporarily need a second file of the same size.

For Studio, keep its existing approved Drive API/method/version and file
identifier/version parameters. This change does not negotiate versions, resolve
a Drive path, or certify a new Drive endpoint. A job-bound grant must also carry
its original `JobID`. Increase the Drive profile's limit and renew approval and
consent. Update all existing `CallRaw` consumers, including MailPlus, to pass a
writer. Lost socket replies can leave completed staging for orphan cleanup; do
not automatically retry an uncertain operation.

The CLI manifest validator must also accept 2147483648 before building an App
with a larger profile. Older CLI versions hard-code a 67108864 maximum and reject
the manifest before the SDK runs. Update the CLI validator and its boundary tests
together with the consuming App's SDK/executor pins.

MailPlus v10 adapter: DSM `synowebapi --exec` appends a separate error-117
JSON reply after its CGI MIME stream. Those bytes are not part of the original
message. For exactly `SYNO.MailClient.Message.get_original` v10, the SDK uses
`outfile=/proc/self/fd/3` with an inherited pipe: the handler writes the original
MIME there, while stdout carries only the bounded JSON control reply. The known
117 control reply is accepted only after nonempty body output; no MIME suffix
is guessed or stripped. This adapter declares `message/rfc822`. Other raw calls
retain the bounded CGI stdout path and need their own compatibility probe before
shipping. Caller-supplied `outfile` is permanently refused in both modes.

Upgrade backend/frontend exact pins together and rebuild the App after the
coordinator authorizes a release. This change does not publish one. A changed
profile policy requires administrator approval and fresh user grants. The
optional profile is declared by the consuming App; no template overlay is needed.
