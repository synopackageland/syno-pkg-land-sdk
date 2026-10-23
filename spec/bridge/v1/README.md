# Bridge channel v1

Status: frozen (2026-10-13). Changes to v1 are additive only: new optional
manifest members, new services, new frame members a v1 peer may ignore
are listed here before use. Anything else is v2. The key words MUST, MUST NOT, SHOULD and MAY are to
be interpreted as described in RFC 2119.

The bridge lets an App written in another language (Python, Node, Rust) serve
the package's API. The Go runtime (`../../../bridge`) stays the CGI: it checks the
origin, the DSM session, the entry gate, the route policy and the deadline with
the `api` package, then hands the verified call to the App over this channel.
The App asks back for services; it never receives the DSM session.

| File | Contents |
|---|---|
| `frames.schema.json` | JSON Schema (draft 2020-12) of every frame, one `$defs` entry per frame |
| `errors.json` | Codes the runtime adds to the `api` codes, with the text in every UI locale |
| `manifest.schema.json` | JSON Schema of `bridge.json`, the App manifest |

## App manifest and package layout

The package build (CLI) writes the App directory, and the runtime reads it at
every CGI start. Nothing in it comes from a request.

```
/var/packages/<package>/target/
  ui/cgi/api.cgi          the runtime (bridge/cmd/runtime), the package's CGI
  app/                    the App directory: never under ui/, which DSM serves
    bridge.json           the manifest (manifest.schema.json)
    permissions.json      method policy, api's format (schema 1, as for Go Apps)
    <entry>               the App: script, module or executable
/var/packages/<package>/var/   the data directory, kept across upgrades
```

1. The runtime finds the App directory from the package ID it was built with:
   `/var/packages/<package>/target/app`. The build binds the SHA-256 of
   `bridge.json` and of `permissions.json` into the runtime (`-ldflags -X`);
   the runtime MUST refuse to start when either file differs.
2. `bridge.json` MUST validate against `manifest.schema.json`; the runtime
   MUST refuse an unknown member, so a v1 runtime never ignores something a
   later build relies on.
3. The runtime starts the App, as the package account, in the App directory:

   | `runtime` | command |
   |---|---|
   | `python` | `/usr/bin/python3 <entry>` (DSM's system Python, 3.8 or later) |
   | `node` | `/var/packages/<node.package>/target/usr/local/bin/node <entry>` |
   | `rust` | `<entry>` |

4. `methods` are the App's routes; `permissions.json` sets their policy (an
   unlisted method needs an administrator). `access` adds the entry gate:
   `admin-only` admits only administrators, `app-privilege` follows DSM's
   Application Privileges for the App.
5. The App's environment is exactly: `PATH=/usr/local/bin:/usr/bin:/bin`,
   `SYNOPKGLAND_BRIDGE=stdio`, `SYNOPKGLAND_PACKAGE=<package>`,
   `SYNOPKGLAND_DATA_DIR=/var/packages/<package>/var`, for `python` also
   `PYTHONUTF8=1`, `PYTHONDONTWRITEBYTECODE=1` and `PYTHONNOUSERSITE=1`,
   and then `env` from the manifest, which cannot override these. Nothing is
   inherited from the CGI.
6. `capabilities` are SDK routes the runtime serves itself in Go (`nas.*`,
   `identity.*`, `portal.status`, and with `root` `executor.status` and
   `executor.control`, see Executor routes); App methods never use SDK method
   names, so a manifest that lists one of these is refused.
7. `capabilities` also offer the App services (Services, added in v1), and
   `delegate` the browser its `delegate.*` routes. `transfers` and
   `max_upload_bytes` (added in v1) make methods HTTP transfers (Transfers).


## Transport

1. A channel is a byte stream: the App's stdin/stdout, one process per call
   (mode `exec`). The App learns the mode from `SYNOPKGLAND_BRIDGE`, which is
   `stdio` in v1.
2. A frame is one line of UTF-8 JSON ending in `\n`. A frame MUST NOT exceed
   16 MiB + 64 KiB including the newline; the runtime treats a longer one as a
   protocol error.
3. A channel carries exactly one call. The runtime closes it after `result`.
4. Reserved: mode `socket` (a resident App on a Unix socket, value
   `socket:<path>`) is kept for a later, opt-in version. v1 runtimes and SDKs
   do not implement it, and a manifest that asks for it is refused.
5. In mode `exec`, the runtime MUST start the App with only the environment the
   package declares plus `SYNOPKGLAND_BRIDGE=stdio`, never the CGI environment
   (it holds `HTTP_COOKIE` and the SynoToken), in a process group of its own,
   and kill that group when the channel closes.
6. Neither side runs as root: the runtime refuses to start the App as root,
   and the SDKs refuse to run as root.

## Sequence

```
runtime → app   {"t":"hello","versions":[1]}
app → runtime   {"t":"hello","version":1,"sdk":"python/0.10.11","methods":["hello.greet","hello.save"]}
runtime → app   {"t":"call","id":"1","method":"hello.save","params":{…},"ctx":{…}}
app → runtime   {"t":"svc","id":"s1","op":"files.list","params":{…}}        (0..n)
runtime → app   {"t":"svc_result","id":"s1","ok":true,"result":{…}}         (one per svc)
runtime → app   {"t":"cancel","id":"1","reason":"deadline"}                  (0..1)
app → runtime   {"t":"result","id":"1","ok":true,"data":…}
```

- Version negotiation: the runtime lists the versions it speaks; the App MUST
  answer with one of them. Any other answer ends the channel as a protocol
  error.
- `methods` MUST equal the App's methods in the package's trusted manifest.
  The runtime registers routes from the manifest, never from the App, so an
  App cannot add a route that `permissions.json` did not see.
- `ctx` carries `user`, `admin`, `locale`, `request_id` and `deadline_ms`;
  see the schema. They come from the runtime's verified caller.
- The App MAY send `svc` frames before `result`; the runtime answers each with
  a `svc_result` carrying the same `id`. An `op` the runtime does not offer is
  `unknown_service`.
- `cancel` tells the App the browser's request is over (`deadline` or
  `canceled`). The App SHOULD stop and send `result`; after one second the
  runtime closes the channel (mode `exec`: kills the process group).
- Any frame out of this order, an unknown member, or invalid JSON is a
  protocol error: the runtime answers the browser `internal` and logs it.

## Data

- `params` is the request's `params` value. `data` is any JSON value and goes
  to the browser as `{"success":true,"data":…}`, re-encoded by the runtime
  (HTML-safe escapes, as the Go server).
- Numbers: the Python and Node SDKs give an App values, not literals. `2.50`
  arrives as 2.5, and a JavaScript App loses integers above 2^53. The Rust App
  SDK (`syno-pkg-land-app`) passes the raw JSON and keeps literals. Schemas SHOULD carry 64-bit IDs and
  sizes as strings.
- `data` MUST stay within the `jsonlimits` bounds (depth 32, 65 536
  structural bytes).

## Errors

- `error.code` is the App's own code, matching `^[a-z][a-z0-9_]{0,63}$`, or
  an upper-case code of kind `refusal` or `client` in
  `../../protocol/v1/errors.json` (for example `SCOPE_DENIED` or
  `APP_STOPPED` from a service), which `@synopackageland/sdk` can translate.
  Other upper-case codes, such as the `outcome` kind, are protocol errors
  (`client` codes since 0.10.4). `message` has at most 1024 bytes. The browser
  sees them as `{"success":false,"error":{…}}`.
- The runtime's own refusals are reserved and an App MUST NOT return them:
  `session_required`, `admin_required`, `app_privilege_denied`,
  `auth_unavailable`, `unknown_method`, `cross_origin`, `origin_required`,
  `request_timeout`, `request_canceled`, `request_too_large`,
  `json_required`, `method_not_allowed`, `app_unavailable`. The runtime
  turns them into `internal`.
- `app_unavailable` (`errors.json`): the App could not start, closed the
  channel before `result` (crash or exit), or did not send `hello` within the
  start timeout (10 seconds by default). The browser may retry. A request
  deadline that ends first is still `request_timeout`, as for a Go App.
- `request_timeout` also has an `errors.json` entry of kind `refusal`: the
  text the browser shows, in every UI locale. The `message` stays the api's
  developer text.
- A `svc_result` error carries the runtime's code, which for executor-backed
  services is the `code` of `../../protocol/v1/errors.json`.
- Added in v1: the services' own codes are `errors.json` entries of kind
  `service` (`limit_exceeded`, `read_failed`, `identity_unavailable`). They
  are not reserved: an App may pass one on to the browser as its own code,
  and `errors.json` carries its text in every UI locale.
- `credential_not_ready` (kind `service`, not retryable): a service that
  acts with the request's DSM session got the session cookie without a
  SynoToken (a DSM session restored without a password sign-in, an
  Application Portal page). It is not temporary: the user signs in again. An
  App's own status passes it on, or reports reason `credential_not_ready`,
  which `AuthorizationGate` words as such.
- Since 0.10.4, `errors.json` also carries the text of the executor
  client's `APP_STOPPED` and `APP_STARTING` (kind `service`, retryable): the
  `root.*` services answer them for a stopped package, and in the 10 seconds
  after a start once the executor still does not listen.

## Services

Added in v1 (additive). The runtime offers the services of the capabilities
in `bridge.json`; any other `op` is answered with `unknown_service`. A
service acts for the caller the runtime verified for this call and, where it
needs one, with this request's DSM session; the App never names a user and
never sees the session. Identity (`identity.*`) is not offered as a service.

Every service:

- takes `params` of at most 65 536 bytes, 1 048 576 for `root.run` and
  `root.start` (else `request_too_large`), as a
  JSON object with exactly the members below (else `bad_request`; absent
  `params` is `{}` where no member is required);
- ends with the call: its deadline is the request's, and `cancel` stops it;
- answers codes that match the `svc_result` pattern: an SDK code's `-`
  becomes `_` (`limit-exceeded` arrives as `limit_exceeded`);
- other than `portal.status`, refuses a call without a signed-in caller (a
  `public` method's) with `session_required`.

| Capability | `op` | `params` | `result` |
|---|---|---|---|
| `files: "session"` | `files.list` | as the `nas.files.list` route: `{"path", "offset"?, "limit"?, "sortBy"?, "sortDirection"?}` | as `nas.files.list` |
| | `files.info` | as `nas.files.info`: `{"path"}` | as `nas.files.info` |
| | `files.read` | `{"path"}`: a NAS path the caller may read in File Station | `{"path", "size"}`: a spool file (see Transfers) with the content, readable until the call ends |
| `portal: true` | `portal.status` | none | `{"state", "alias"?, "entry"?}` |
| `delegate: {…}` | `delegate.status` | `{}` | as the `delegate.status` route |
| | `delegate.move_from_staging` | as the `delegate.move_from_staging` route | as that route |
| | `delegate.operation` | as the `delegate.operations.get` route | as that route |
| | `delegate.discard_staging` | `{"delegation_id", "job_id"}` | `{"discarded": true}` |
| `jobs: true` | `bg.submit` | `{"key", "name", "input"}`: `input` is an absolute path of a file in the data directory, at most 512 KiB, read without following links out of it | `{"job", "duplicate"}` |
| | `bg.get` | `{"id"}` | the job; only the caller's own, else `not_found` |
| | `bg.cancel` | `{"id"}` | the job; only the caller's own, else `not_found` |
| `root: true` | `root.run`, `root.start`, `root.jobs.list`, `root.jobs.get`, `root.jobs.stop` | the executor operation's params (`../../protocol/v1`), any JSON object | the executor's result, unchanged |

The default runtime's legacy `files.*` services now return `unsupported`
without replaying the browser's DSM session. Interactive file operations
run directly in the frontend using DSM's same-origin browser transport.
Unused server-only read/import scaffolds remain unsupported; do not replace
them with package-account filesystem access. This applies equally to Python,
Node and Rust wrappers. See [migration](../../../docs/dsm-session-transport.md).
An independently authorized custom file adapter still applies its spool
limit (`max_upload_bytes`, 16 GiB by default) and refuses a larger read with
`limit_exceeded`.

`root.*` forward the operation to the package's executor with this request's
DSM session as `auth`; the App's params cannot carry `auth`. The executor
verifies the session with DSM and decides with its policy and DSM's App
privilege; the runtime decides nothing and waits within the request's
deadline at most 140 seconds for `root.run` and 40 for the other operations
(`../../protocol/v1`, Timeouts). A refusal arrives with the executor's code
(`../../protocol/v1/errors.json`). An operation the executor does not know is
its refusal, not `unknown_service`.

The runtime reaches the executor only at
`/run/synopkgland/<package>/executor.sock` (`../../protocol/v1`): no request,
CGI variable or environment of the package CGI or worker may change that path.
An SDK API for tests (Go `bridge.Config.ExecutorSocket`) may name another
socket in a test process; a package build never sets it.

A `root.run` result with `staging` and `size` (instead of `output`) names a
file the executor wrote for the package (`../../protocol/v1`), owned by the
package account. The runtime passes it on unchanged and does not remove it:
the App reads the file and then deletes it. `staging` is a bare file name
(`root-` and 32 hex digits), not a path; the file is
`$SYNOPKGLAND_DATA_DIR/executor-staging/<staging>`. An App checks the name
before joining it and reads at most `size` bytes.

### Executor routes

Added in v1 (additive). With `root: true` the runtime serves two browser
routes itself, with the SDK's permissions, as a Go App does
(`delegate.ExecutorRoutes`). The package's CLI root window calls them after an
administrator approves the executor.

- `executor.status` (signed-in callers), no params: the executor's own
  `status` result (`../../protocol/v1`), with `executor_digest` and
  `policy_digest`, unchanged. While the executor does not listen, the
  lifecycle mailbox decides: a stopped package refuses with `APP_STOPPED`, and
  one whose start was requested less than 10 seconds ago with `APP_STARTING`
  (as the `root.*` services do); otherwise (its pins await
  approval, or a start found no executor) it answers
  `{"ready": false, "state": "paused", "reason": "executor_not_listening",
  "next"}`, `next` saying which the mailbox shows. Any other refusal arrives
  with the executor's code. An executor that listens
  but still runs its start step answers `ready: false` with `state`
  `"starting"`: it is alive, and a caller keeps waiting.
- `executor.control` (administrators), `{"action"}` of `start`, `stop` or
  `purge` and nothing else (else `bad_request`): writes the action whole to
  the package's lifecycle mailbox `<data>/executor/control`, refusing a
  mailbox that is a link, and answers `{"written": true}`. It does not call
  the executor.

`delegate.*` and `bg.*` use the package's job store in the data directory
(`background/`); the jobs run in the package's background worker.

## Background worker

Added in v1 (additive). `api.cgi --worker` is the package's background
worker: it runs the jobs `bg.*` and `delegate.*` queue in `background/`
until `SIGTERM` or `SIGINT`, the way a Go App's worker runs them. It starts
no App; v1 jobs are the SDK's own.

- It runs as the package account, normally from DSM's systemd user unit,
  and MUST refuse to start as root or with `GATEWAY_INTERFACE` set (a CGI
  start).
- It reads `bridge.json` with the same bound digests as a CGI start and
  refuses a file that differs.
- With neither `capabilities.jobs` nor `capabilities.delegate` it exits with
  an error: the package has no jobs to run, and the build SHOULD NOT start a
  worker for it.

## Transfers

Added in v1 (additive). A method `transfers` names is served as an HTTP
transfer (`api.cgi?method=<method>`), never as a JSON call. Its other query
members, except `method`, `SynoToken` and `_sid` in any case, reach the App as
`params.query` (strings; a repeated member is `bad_request`). The runtime
spools the body in the data directory: `<data>/bridge-spool/<request_id>.<use>`,
a 0600 file in a 0700 directory, removed when the call ends; files older than
two hours are removed at the next transfer.

- `upload`: `POST` with `Content-Type: application/octet-stream` and a
  `Content-Length` of at most `max_upload_bytes` (else `request_too_large`).
  The App gets `params`
  `{"query": {…}, "upload": {"path": "<spool file>", "size": <bytes>}}` and
  answers as for a JSON call.
- `download`: `GET`, or `POST` of an urlencoded form. The App gets `params`
  `{"query": {…}, "download": {"path": "<spool file>"}}`, writes the file
  there, and answers `{"name": "<file name>", "content_type"?: "<type>"}`.
  The runtime sends the file as an attachment with that name and type
  (`application/octet-stream` by default); an App error goes to the browser
  as a JSON error envelope with the HTTP status below, and an answer of
  another shape is `internal`.

HTTP transfer refusals (native Go and the shared Node/Python/Rust runtime) MUST
use an error status before response headers/body start. `not_found`,
`source_deleted`, `unknown_method` map to 404; `forbidden`, `permission`,
`permission_denied`, `denied`, `admin_required`, `app_privilege_denied`,
`cross_origin`, `origin_required` to 403; `unauthorized`, `session_required` to
401; `bad_request` to 400; `request_too_large` to 413; `auth_unavailable`,
`unavailable`, `service_unavailable`, `request_timeout`, `request_canceled` to
503; other codes to 500. The body keeps `success:false` and the public error
code/message/details. Prepared attachment headers MUST be removed. JSON calls
retain HTTP 200 error envelopes. Once a transfer response starts, errors MUST
NOT append JSON or replace its status. DSM nginx may replace CGI error bodies;
clients MUST check the HTTP status before treating a response as a file.

### Profile WebAPI services (SDK 0.11.0)

`capabilities.webapi:true` offers `webapi.grant`, `webapi.grants`,
`webapi.revoke`, and `webapi.call` through the existing `Context.runtime` channel
in Node/TypeScript, Python and Rust. It also serves executor.status/control for
approval. It does not add root program services. The root-owned executor policy
must independently declare the named profiles and operation; the bridge manifest
cannot broaden it. Every service requires a signed-in caller and forwards that
caller's live session/address. `webapi.grant` takes `scope` and TTL/job or
until-revoked lifetime, and fixes ops to [webapi.call]. `webapi.call` uses the
protocol params; the executor verifies that the grant belongs to the caller.
`webapi.grants` lists the caller's grants and `webapi.revoke` takes delegation_id.
Go background workers use delegate.JobContext.WebAPI().Call without credentials.
See executor/README.md for denylist, bounds, audit and uncertain mutation rules.

## Negotiated request batches

The optional `batch:1` hello capability enables the bounded extension in
[Request batches v1](../../batch/v1/README.md). It sends up to eight call frames
in one batch frame and receives independent result frames on the same process.
Service frames must carry the current `batch_id` so a public method cannot
borrow another method's verified Caller. Without negotiation, the original
single-call exchange is unchanged.
