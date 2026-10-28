# WebAPI request uploads

`webapi.call` params are capped at 32768 encoded bytes so a request fits the
64 KiB executor frame. An App whose background job must send a small file in one
API parameter (for example Synology Drive `SYNO.SynologyDrive.Files` `create`
v2 `file_content`, a base64 string documented up to 1 MB) cannot do so inline.
Uploads are the request-side counterpart of [raw responses](webapi-raw.md):
the App stages the bytes in a private folder and passes only a handle.

## Limit

A named administrator-approved profile opts in with one optional
`upload_max_bytes` integer (1..96000). Omitting it or setting zero keeps the
profile without uploads and its old digest (the field is `omitempty`). Setting or
changing it changes the policy and profile digests: administrator approval and
fresh user consent are required.

Why 96000: the executor passes each param to `/usr/syno/bin/synowebapi --exec`
as one argv string `name=<json>`, and Linux refuses one string of 131072 bytes or
more (MAX_ARG_STRLEN, counting the NUL). 96000 bytes encode to exactly 128000
base64 bytes; adding two JSON quotes, a name of at most 64 bytes, `=` and the NUL
gives at most 128068, under 131072. The executor also refuses, never truncates,
any argv string at or over that bound. The limit is below the 1 MB Drive allows.

## Request

```json
{"delegation_id":"...","api":"SYNO.SynologyDrive.Files","method":"create","version":2,
 "params":{"path":"/mydrive/report.xlsx"},
 "upload":{"param":"file_content","staging":"app-0123456789abcdef0123456789abcdef",
           "size":5120,"sha256":"<64 lowercase hex>"}}
```

Exactly one param per call. The executor sets `params[param]` to the standard
base64 of the file before building argv; the caller never sends the base64.

## Staging

The App writes the bytes to `/var/packages/<package>/var/executor-upload/`
(directory 0700, file 0600, named `app-<32 lowercase hex>`) and sends only the
bare handle. The root executor opens the directory and file without following
symlinks, requires a regular file owned by the package account with one hard
link and no group/other access, checks size equals the declared size and the
limit, reads at most the limit plus one byte, verifies SHA-256, and unlinks the
file whatever the outcome, including refusals after the handle is well formed.
Content and handle path are never logged. The audit records mode (`:upload`) and
the declared byte count only.

## Go

```go
job := delegate.JobContext{Client: delegate.Local(packageID),
    DelegationID: grant.ID,
    UploadDir: "/var/packages/" + packageID + "/var/executor-upload"}
out, err := job.WebAPI().CallUpload(ctx, "SYNO.SynologyDrive.Files", "create", 2,
    map[string]any{"path": "/mydrive/report.xlsx"}, "file_content", xlsx)
// out is the DSM envelope, exactly as Call returns it. The staged file is
// removed whether the call succeeds, fails or is uncertain. Not retried.
```

`CallUpload` creates `UploadDir` (0700) if missing, refuses a folder others can
access, and refuses data over 96000 bytes, an empty `param`, or a `param` already
in `params`. Only Go has this client; there is no bridge, TypeScript or Python
counterpart (as for `CallRaw`, bridge Apps use their `webapi.call` service and
would need to stage the file themselves).

## Refused

- profile without `upload_max_bytes`, or size over it: `POLICY_DENIED`
- `upload` together with `response_type:"raw"`: `INVALID_REQUEST`
- `param` invalid, reserved (`sid`, `runner`, `token`, ...) or also in `params`;
  malformed handle, missing/negative/over-96000 size, bad SHA-256, unknown
  `upload` keys: `INVALID_REQUEST`
- file missing, symlink, not regular, wrong owner, extra hard link, loose mode,
  size or SHA-256 mismatch: `INVALID_REQUEST`; nothing is sent to DSM
- caller `params` over 32768 bytes: `REQUEST_TOO_LARGE` (the cap still applies
  to the caller's own params; only the injected param is held to 128000 base64
  bytes in the executor's recheck)

An upload does not widen the API/method/version allowlist, the permanent
denylist (authentication, File Station, administration, ...), or the runner: the
call is still made as the consenting user. A tampered or replaced handle cannot
reach another App's files because the folder is the package's own.
