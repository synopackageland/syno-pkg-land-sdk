# Executor protocol v1

Since 0.9.0. The key words MUST, MUST NOT, REQUIRED, SHALL, SHALL NOT, SHOULD,
SHOULD NOT, RECOMMENDED, MAY and OPTIONAL are to be interpreted as described in
RFC 2119.

The executor is a root process that a DSM package ships next to its app. The
app asks it to act as a signed-in DSM user over a Unix socket. This directory
is the protocol: the executor in `../../../executor` and the Go client in
`../../../delegate` both conform to it, and `../../../conformance` checks
the executor against the vectors here.

| File | Contents |
|---|---|
| `schema/<op>.json` | JSON Schema (draft 2020-12) of the `params` of each op, with `since` |
| `errors.json` | Every code: `code`, `kind`, `retryable`, `httpStatus`, `en`, `zh-TW`. The only source of codes; the Go constants are generated from it |
| `vectors/*.jsonl` | One request per line: `{name, request, policy, state, expect}` |
| `timeouts.json` | The timeouts of the section Timeouts, in seconds; the SDK's constants and tests read it |

## Transport

1. The socket is `/run/synopkgland/<package>/executor.sock`. The executor MUST
   accept a connection only from the package user or root (peer credentials).
2. A connection carries exactly one request and one response.
3. A request is one line of UTF-8 JSON ending in `\n`. A request line MUST NOT
   exceed 64 KiB including the newline, or 1 MiB for `root.run` and
   `root.start`; the executor answers `REQUEST_TOO_LARGE`.
4. The executor MUST answer one line of JSON ending in `\n` and close.
5. The executor SHOULD close a connection that sends nothing for 15 seconds.

## Timeouts

Since 0.10.3. Every layer between the browser and a root program waits
longer than the layer it calls, so the inner answer, or the inner refusal,
arrives before the outer layer gives up. The margin is 10 seconds between
the executor and its client and between the client and the request; the
browser waits 2 seconds past the request (`timeouts.json` holds the values).

| Layer | Waits for | Bound |
|---|---|---|
| Root program (`run.timeout_s`) | its own work | 1–120 s, default 20; the process group is then killed |
| Executor, `root.run` | the program, then its reply | answers within 130 s (120 + 10) |
| Executor, every other op | its DSM calls | answers within 30 s |
| Executor client (Go `delegate.Client`, the bridge runtime's `root.*` and `delegate.*`) | the executor | `root.run` 140 s (`delegate.RootRunTimeout`), every other op 40 s (`delegate.ExecutorTimeout`) |
| Request (`api.Timeout`, manifest `timeout_seconds`) | the App's backend | default 60 s (`api.DefaultRequestTimeout`), 1–3600 |
| Browser (`clientTimeout`, the CLI window's `call`) | the request | the request's deadline + 2 s |

- The request's deadline ends every inner wait: a client never waits past it.
  An App whose backend awaits `root.run` therefore needs a deadline of at
  least the program's `run.timeout_s` + 20 seconds (the executor's 10 and the
  client's 10), so a 120-second program needs `timeout_seconds` 140; a build
  tool SHOULD refuse less. The default 60 covers programs of up to 40 seconds.
- A client that gave up does not stop the work: `root.run` runs on (it cannot
  be canceled), and `portal.register` and `portal.unregister` finish their
  steps within 2 minutes. The client reads the result again (`status`,
  `root.jobs.get`) instead of repeating the work.
- The start step (Package start step) is waited on by no request: while it
  runs (at most 60 seconds) every op but `status` is refused at once with
  `EXECUTOR_STARTING` (retryable). A window that waits for the executor to
  become ready polls `status` for longer than 60 seconds before it reports
  the start step as still running.

## Request

```json
{"v":1,"op":"files.moveFromStaging","auth":{"dsm_cookie":"…","dsm_synotoken":"…","remote_addr":"192.0.2.1"},"params":{…}}
```

- `v` MUST be `1`.
- `op` MUST be one of the ops with a schema in `schema/`. An unknown op is
  `UNKNOWN_OP`; an op outside the approved policy is `POLICY_DENIED`.
- `auth` is OPTIONAL. When present it MUST contain only `dsm_cookie`,
  `dsm_synotoken` and `remote_addr` (since 0.9.4), the browser's address
  (REMOTE_ADDR). DSM's Application Privileges are evaluated for that address;
  an op that checks them MUST be refused with `APP_DENIED` when a present
  `auth` has no usable `remote_addr`. A grant records the `remote_addr` of the
  `delegate.create` that made it (creating it again records the new one), and
  an operation records the address of its `files.moveFromStaging` (since
  0.9.6). A request without `auth` under a grant, and the operation's
  background revalidation, are checked from that recorded address; work with no
  recorded address is checked as coming from 127.0.0.1. A present `auth` is
  always treated as caller authentication, never ignored, even when empty. Ops whose schema says
  `"auth": "required"` MUST be refused with `AUTH_DENIED` without a valid DSM
  session.
- `params` MUST be an object valid against `schema/<op>.json`. A member that
  belongs to another op is `INVALID_REQUEST`.
- The executor MUST refuse with `INVALID_REQUEST`: invalid UTF-8, duplicate
  keys (compared with Unicode case folding), keys in `params` or `auth` other
  than lowercase ASCII, digits and `_`, unknown members, and trailing data
  after the object.

## Response

```json
{"ok":true,"result":{…}}
{"ok":false,"code":"SCOPE_DENIED","message":"scope denied"}
```

- `ok` is REQUIRED. On success `result` MUST be an object.
- On refusal `code` MUST be a code of kind `refusal` in `errors.json`.
  Clients MUST match on `code`, never on `message`.
- A client MUST treat a reply that is not a JSON object with a boolean `ok`
  as `EXECUTOR_PROTOCOL`, and an unreachable socket as
  `EXECUTOR_UNAVAILABLE`. Codes of kind `client` are never sent by the
  executor.
- A client that knows the package's lifecycle mailbox (`<data
  dir>/executor/control`) answers an unreachable socket with `APP_STOPPED`
  when its last request is `stop`; within 10 seconds of a `start` it waits
  for the socket and ends with `APP_STARTING`. Neither sends a byte, so the
  caller MAY repeat the request.
- Such a client answers `EXECUTOR_UNAVAILABLE` only for a package that runs
  and was not started in the last 10 seconds, yet whose executor does not
  listen: it is not approved, awaits approval for a changed policy, or failed.
  `retryable` says that nothing was sent, not that waiting helps: a browser
  client repeats `APP_STARTING` and `EXECUTOR_STARTING` on its own for a few
  seconds (the DSM window: 4 s), since a start ends by itself, but shows
  `APP_STOPPED` at once, since only the user starts a stopped package, and
  shows `EXECUTOR_UNAVAILABLE` as the approval state at once, since only an
  administrator's approval (which also restarts the executor) changes it.

## Status

`status` answers `version` (`"1"`), `ready`, `disabled`, `executor_digest`
and `policy_digest`. With `auth` (since 0.9.6) it also answers `allowed`:
whether DSM's Application Privileges let the caller use the App and File
Station from `remote_addr`, the check `delegate.create` makes; an invalid
session is `AUTH_DENIED`. A CGI sends the caller's `auth` so an App the
executor would refuse is never shown as ready. While a due start step runs
(see Package start step), `status` answers `ready: false` with `state`
`"starting"` and both digests. The executor MUST NOT report the SDK version; the binary
digest MUST change only when the executor source changes.

`status` also answers `tasks`, the executor's own readback of
its approval tasks (`../../scheduler/v1`): `{"state", "checked_at"}`. `state`
is `checking` until the first readback after the executor started ends, then
`ready` (both tasks are exactly the canonical ones under this executor's
policy digest), `stale` (a `ready` readback older than 15 minutes) or `failed`
(any other verdict, or DSM did not answer within 150 seconds). `checked_at`
(RFC 3339, UTC, the NAS clock) is when that readback started; `checking` has
none. The executor reads back at start, every 10 minutes after a pass and
every minute after a failure; a `stale` status starts a readback. The
executor judges freshness itself, so a client need not compare `checked_at`
with its own clock. An older executor answers no `tasks`: a client then reads
the tasks back itself.

## Application Portal

Since 0.9.6. A policy MAY carry `"portal": {"application": "<display name>",
"alias": "<default alias>"}` together with both `portal.register` and
`portal.unregister`; a policy with only some of the three is invalid. The
executor then registers the App's Application Portal entry the way DSM's
web-config worker does, on every DSM version: an App whose `os_min_ver` is
below 7.2 cannot declare that resource, since DSM 7.1 refuses it for unsigned
packages.

- Both ops take no params and require the session of an administrator whom
  Application Privileges allow to use the App (`ADMIN_REQUIRED`,
  `APP_DENIED`). The alias itself stays an administrator's
  `SYNO.Core.AppPortal set`; the executor never writes it.
- `portal.register` writes `/usr/syno/etc/www/app.d/<package>.mustache`, then
  `<package>.json` (id `SYNO.SDS.<package>.Instance`), runs
  `synow3tool --gen-app=<package>`, and requires `nginx -t` and the App in
  `SYNO.Core.AppPortal list`. Registering again rewrites the same files.
- `portal.unregister` removes both app.d files and
  `/usr/syno/etc/packages/<package>/AppPortal.json`, disables and
  unregisters every `{dsm,www,alias,.alias}.syno-app-portal.<package>.conf`
  conf and `{server,.server}` site still enabled or still in DSM's registry,
  and reloads nginx once `nginx -t` passes. A step with nothing to remove is
  skipped, so the op MAY be repeated.
- A failed change is `PORTAL_FAILED`. A failed register is undone the same
  way. Until a later `portal.unregister` succeeds, `status` reports
  `portal` `cleanup-required`.
- The executor MUST remove the entry when it purges for an uninstall; DSM
  does not.
- `status` answers `portal` for such a policy: `registered` (the executor's
  own files and DSM's generated conf), `unregistered`, or `cleanup-required`
  (anything else, including a failed change).

## Root programs

Since 0.10.0. Each `root` entry pins a program by its SHA-256 digest; a bare
digest is a script `root.run` may run with the defaults. The object form is
`{"digest", "kind", "schema", "run"}`: `kind` `script` (`root/<name>.sh`) or
`binary` (`root/<name>`), `schema` a closed JSON Schema subset for its params,
`run` its `timeout_s` (1–120, default 20) and `max_output` (≤ 64 MiB,
default 1 MiB), `job` its background limits: `timeout_s` (1–604800, default
86400), `grace_s` (1–60, default 10), `max_concurrent` (1–4, default 1) and
`nice` (0–19, default 10). `executor/README.md` lists the schema keywords.
`root.run` needs `run`, `root.start` needs `job`; an entry may have both.

Every root program, whatever starts it:

- runs as root in `/`, in its own process group, without core dumps, with
  stdin the params JSON (`{}` without params) followed by EOF;
- gets only `PATH=/usr/syno/bin:/usr/syno/sbin:/usr/bin:/bin`, `LANG=C`,
  `HOME=/` and these variables:

| Variable | Set for | Value |
|---|---|---|
| `SYNOPKGLAND_PACKAGE` | all | the package name |
| `SYNOPKGLAND_ROOT_MODE` | all | `run`, `job`, `start`, `stop` or `purge` |
| `SYNOPKGLAND_REQUESTED_BY` | run, job | the verified DSM user name |
| `SYNOPKGLAND_REQUEST_ID` | run | 64 hex digits, unique per call |
| `SYNOPKGLAND_JOB_ID` | job | the job's `job_id` |
| `SYNOPKGLAND_CONTROL_FD` | job | `4` |
| `SYNOPKGLAND_PROGRESS_FD` | job | `5` |
| `SYNOPKGLAND_ROOT_STATE` | all, with `root_state` | the root state directory |

- `root.run` MUST require an administrator session whose Application
  Privileges allow the App, and an entry with `run`; params outside the
  schema, or any params without one, are `INVALID_REQUEST`.
- The executor MUST give the params to the program only as JSON on stdin,
  and MUST set `SYNOPKGLAND_REQUESTED_BY` to the verified user name. The
  environment holds nothing else from the caller.
- It MUST execute the descriptor whose content it checked against the digest,
  never the path again.
- A program that exits, whatever its status, is a result: `request_id`,
  `exit_code`, `stderr` and either `output` (UTF-8, in a reply under 64 KiB)
  or `staging` and `size`, a package-owned staging file the client reads and
  removes. `staging` is a bare file name, `root-` and 32 lowercase hex
  digits, never a path: the client joins it to the package staging folder,
  `/var/packages/<package>/var/executor-staging`. The executor MUST create
  that folder, owned by the package account with mode 0700, when it is
  missing; a staging folder it cannot use is `ROOT_FAILED` with the message
  `root output staging unavailable`. Past the timeout the process group is killed (`ROOT_TIMEOUT`);
  output past `max_output` is `limit-exceeded`; a full staging quota is
  `ROOT_STAGING_FULL`; two running programs make a third `ROOT_BUSY`.
- A `root.run` request MAY be up to 1 MiB; the executor answers within 130
  seconds and a client SHOULD wait 140 (Timeouts).
- `root.run` cannot be canceled: a closed connection does not stop the
  program, which runs until it exits or its timeout. Work that may be stopped
  is a root job (`root.start`, `root.jobs.stop`).

## Root jobs

Since 0.10.0. `root.start`, `root.jobs.list`, `root.jobs.get` and
`root.jobs.stop` come together in `operations`. Each requires an administrator
session whose Application Privileges allow the App, like `root.run`; any such
administrator sees and stops every job of the package.

| op | params | result |
|---|---|---|
| `root.start` | `name`, `params` (object, optional) | `{"job_id", "state": "running"}` |
| `root.jobs.list` | none | `{"jobs": [summary, ...]}`, newest first |
| `root.jobs.get` | `job_id` | job |
| `root.jobs.stop` | `job_id` | job, `stopping` or already ended |

A job is:

```json
{
  "job_id": "<64 hex digits>",
  "name": "clean",
  "state": "running",
  "requested_by": "alice",
  "started_at": "2026-10-13T05:00:00Z",
  "ended_at": "2026-10-13T05:03:12Z",
  "exit_code": 0,
  "cancel_reason": "user",
  "progress": {"percent": 40, "message": "scanning", "phase": "scan"},
  "result": {"freed": 3},
  "output": "first 4 KiB of stdout and stderr"
}
```

- `state` is `running`, `stopping` (a cancel was sent), or an end:
  `succeeded` (exit 0), `failed` (another exit or a signal), `canceled` (it
  ended after a cancel, however it exited), `timed-out` or `interrupted`
  (the executor ended without seeing it end). `ended_at`, `exit_code` (an
  exit, not a signal) and `cancel_reason` appear once they apply; `progress`
  and `result` once the job reported them. `output` is valid UTF-8.
- A summary is a job without `result`, `output` and the progress `message`
  and `phase`, so the 64 records fit a reply; `root.jobs.get` answers the
  whole job.
- `root.start` MUST refuse with `ROOT_BUSY` while the entry runs
  `max_concurrent` jobs or the package runs four. Params follow the entry's
  schema as for `root.run`. An entry without `job` is `POLICY_DENIED`.
- An unknown `job_id` is `ROOT_JOB_NOT_FOUND`. The executor keeps the 64
  newest records; older ones are removed.
- `root.jobs.stop` of an ended job returns it unchanged.
- Jobs do not survive the executor: when it stops, every running job is
  canceled, and a job a crashed executor left is `interrupted` (its process
  group is killed when the same process still runs).

### Progress and cancel

The job reports on fd 5 and is told to stop on fd 4; both are pipes.

- **Progress** (fd 5, job to executor): JSON lines of at most 32 KiB, each an
  object with any of `percent` (number 0–100), `message` (string, at most 512
  characters), `phase` (string, at most 64 characters) and `result` (any JSON,
  at most 16 KiB encoded). Each member present replaces the previous one; a
  line with anything else is ignored whole. `result` is the job's answer for
  its caller, kept after it ends.
- **Cancel** (fd 4, executor to job): one line `{"cancel":"<reason>"}`, with
  reason `user` (`root.jobs.stop`), `stop` (package stop), `upgrade`
  (package upgrade), `purge` (uninstall) or `executor-stop`. EOF on fd 4
  means the executor ended: the job should stop too. The job ends
  gracefully; `grace_s` after the cancel (at most 15 seconds on package stop,
  upgrade and uninstall) the executor kills its process group.
- **Timeout**: `timeout_s` after the start the process group is killed
  without a cancel (`timed-out`).
- A job MAY also keep its own progress files (for example in
  `SYNOPKGLAND_ROOT_STATE`) for its App to read; the executor does not
  interpret them.
- Clients poll `root.jobs.get`; each answer is the job's latest progress. A
  bridge MAY turn successive answers into progress events.

## Package stop step

Since 0.10.0. A policy MAY carry `"stop": "<name>"`, naming a `root` entry the
executor runs when the package stops, after its jobs ended, with no session.
Its argument is `prepare-upgrade` when the package account wrote `upgrade`
to `var/executor/stop-reason` before the stop request (the
`executor/lifecycle stop --upgrade` helper does; DSM's `preupgrade` should
use it), and `stop` otherwise; jobs are canceled with `upgrade` or `stop`
accordingly. It runs for at most 30 seconds, only while the approval is live
(as for the purge step), its exit and output go to the executor log, and a
failure does not stop the stop. `root.run` and `root.start` refuse it with
`POLICY_DENIED`, so a policy whose stop or purge entry declares `run` or
`job` is invalid; the entry is a lifecycle step only, either
with neither member or as the shorthand digest. Session work takes another
entry, which may pin the same digest under another name.

## Package start step

Since 0.10.3. A policy MAY carry `"start": "<name>"`, naming a `root` entry
the executor runs once after each install or upgrade, with no session:

- The executor MUST run it at the first start of a package version (the
  `version` of the package INFO), after the pins matched. The socket opens first
  and the step runs once the executor has read the
  approval tasks back; until the step ends, `status` answers `ready: false`
  and `state` `"starting"`, and every other request is refused with
  `EXECUTOR_STARTING` (retryable), so no session reaches the App's root work
  while the step runs. Its
  arguments are `install` when the executor has recorded no earlier start,
  and `upgrade` followed by the version recorded before otherwise. Every
  start records its version, also for a policy without the step; a package
  that adds the step in an upgrade therefore gets `upgrade`, except on the
  first start under an executor that predates the record, which is `install`.
- It runs only while the approval is live (as for the purge step); otherwise
  it MUST log `start root step skipped: not approved` and run at the next
  start. A step cut short because the executor ended also runs again.
- It runs for at most 60 seconds; its exit and output go to the executor log,
  and the executor MUST become ready whatever it answers. A failure is the result of
  that version: the step does not run again until the next upgrade or
  reinstall, so the program should be idempotent.
- `status` answers `start_step` for such a policy: `{"state": "pending"}`
  until the step ran for the version being served, then `state` `ok` (exit
  `0`) or `failed`, `reason` (`install` or `upgrade`), `previous_version` for
  an upgrade, `exit` (an exit code, `timeout`, `signal` or `refused`),
  `output` (at most 4 KiB, which the package account can read) and
  `finished_at` (RFC 3339).
- As for stop and purge, `root.run` and `root.start` refuse it with
  `POLICY_DENIED`, and an entry that declares `run` or `job` makes the policy
  invalid.

Because DSM's package start only writes the start request, `postinst`,
`postupgrade` and `start` scripts run before or while the step runs; they
MUST NOT expect its result. The App reads it from `status`.

## Root state directory

Since 0.10.0. A policy with `"root_state": true` gives its root programs a
directory only root can read: `/volumeN/@synopkgland/<package>`, root:root
0700, on the volume that holds the package's `var` (DSM's
`/volumeN/@appdata/<package>`); `/volumeN/@synopkgland` is root:root 0755.
The executor creates it at start and passes it as `SYNOPKGLAND_ROOT_STATE`;
when the package `var` is not on a volume it MUST NOT start root programs
that need it (`ROOT_FAILED`). The directory is outside every package
directory, so upgrades keep it. At uninstall the purge step sees it; after
the step the executor deletes it for `delete-state` and keeps it for
`keep-state`, so a reinstall on the same volume finds it again.

## Uninstall purge step

Since 0.9.7. A policy MAY carry `"purge": "<name>"`, naming one of its `root`
ops as the App's root step at uninstall; any other value makes the policy
invalid. Approving that policy is the administrator's consent that the op
runs as root at uninstall, with no session.

- `root.run` MUST refuse that op with `POLICY_DENIED`: a session cannot start
  the uninstall step early.
- When the package account requests purge, the executor MUST run the op
  before it removes Application Privileges rules, its portal entry and its
  approval tasks, and only while the approval is live: the package ships the
  approved executor and policy pins, and both approval tasks of that policy
  stand with their exact scripts. Otherwise, and on an offline purge, it MUST
  skip the op and log `purge root step skipped: not approved`.
- The op gets the argument `delete-state` when the package account wrote
  `delete-state` to `var/executor/uninstall` before its request (the
  `executor/lifecycle purge --delete-state` helper does), and `keep-state`
  otherwise. It runs for at most 60 seconds; its exit status and output go to
  the purge log, and a failure MUST NOT stop the rest of the purge.

## Grants

- A time-limited grant (`job_id`, `ttl_seconds` ≤ 30 days) covers the listed
  `files.*` ops below `path`.
- An until-revoked grant (`destination`, `lifetime: "until-revoked"`) covers
  only `files.moveFromStaging` into one directory. A new grant for the same
  user and destination MUST revoke the previous one (`destination-replaced`).
  Creating it again for the same directory MUST return the active grant
  unchanged, so a client retrying a create it timed out on keeps its saves.
- Every operation under an until-revoked grant MUST revalidate the account,
  Application Privileges, scope, directory identity and write permission
  against DSM. A failure the user or an administrator can undo MUST fail only
  that operation and MUST keep the grant: `PERMISSION_DENIED` (the ACL refuses
  the write), `not-found` (the granted folder is not at its path) and, since
  0.9.6, `APP_DENIED` (an Application Privileges rule refuses the account from
  the checked address). Once the cause is fixed, a new operation under the same
  grant succeeds. Any other revalidation failure (`DELEGATION_DENIED`,
  `INELIGIBLE`, `SCOPE_DENIED`, including a different folder at the granted
  path) MUST revoke the grant; the user then chooses the destination again.
- `files.taskStatus` and `files.taskCancel` follow the task's op and MUST NOT
  be granted directly.
- An administrator MAY turn the integration off with `delegate.disable`; then
  `delegate.create` and every `files.*` op MUST be refused with `disabled`.

## Asynchronous operations

`files.moveFromStaging` admits an operation keyed by `job_id`. Sending the same
`job_id` with the same input MUST return the existing operation; different
input MUST be `operation-conflict`. States: `queued`, `copying`, `committed`,
`canceled`, `interrupted`, `uncertain`. `files.operationStatus` and
`files.cancelOperation` report the operation; `code` and `reason` in it are
codes of kind `outcome` and `reason`.

## Vectors

Each line of `vectors/*.jsonl`:

| Member | Meaning |
|---|---|
| `name` | Unique within its file |
| `request` | The request object, or a JSON string sent as the raw line (for malformed input) |
| `policy` | The approved policy the executor runs with |
| `state` | Executor state before the request: `disabled`, and `grants` (`id`, `username`, `key`, `path`) as until-revoked home grants |
| `expect` | The response. Objects match on the members listed; `"<any>"` matches any present value |

The vector DSM knows `alice` (uid 1205), `bob` (1206) and `admin` (1207, an
administrator). Their `dsm_cookie` is the user name and `dsm_synotoken` is
`contract-token`.

## Versioning

New ops and members are added with a `since` of the SDK release. The protocol
`v` changes only for an incompatible envelope change.

## JSON WebAPI grants (since 0.11.0)

`webapi.call` MUST execute only an exact API/method/version in the grant's named
`webapi` policy profile, with `runner` fixed to its owner. A profile has `purpose`
and `apis` (API -> method -> integer version array). It is part of the approved
policy digest. Grants MUST additionally pin their profile and revalidate the
account, UID, Application Privileges and original-address rules on every call.
`delegate.create` with only `webapi.call` uses `scope` as the profile name and
MUST omit file path/destination fields. It accepts job-bound TTL up to 30 days
or `lifetime:"until-revoked"`; all other grant rules remain unchanged. Mixing
file and WebAPI operations MUST be refused. Revoke and the integration disable
switch MUST fence WebAPI admission and drain admitted calls before acknowledging.

Core, Auth, API/Entry dispatch, FileStation and administrative namespaces/methods
MUST be refused even if declared (executor/README.md lists the permanent denylist).
Routing, runner, authentication and identity arguments MUST be refused. The
encoded params object MUST be at most 32 KiB, the JSON DSM envelope at most
48 KiB, and the call deadline at most 10 seconds including account revalidation.
Duplicate JSON keys MUST be refused. The wire frame remains at most 64 KiB.
Every admitted call MUST be audited without recording params/results.

Protocol success returns the full DSM success/error envelope. No automatic
mutation retries are permitted. A dispatched call whose execution/reply/audit
cannot be confirmed MUST answer `WEBAPI_UNCERTAIN` (nonretryable); client
transport failure MAY also hide a completed operation. Binary/streaming replies
MUST be refused. These bounds and semantics also apply to until-revoked grants.

## Raw WebAPI extension (since 0.12.1)

The JSON-only restriction above applies to the default response mode. An explicit
`response_type:"raw"` MUST require a positive, approved profile `raw_max_bytes`
(1..67108864), pinned by policy and user consent. The executor MUST parse bounded
CGI headers, refuse redirects, enforce the body limit without truncation, and
return `response_type`, `content_type`, `size`, `sha256` and exactly one of
base64 `body` (at most 32768 bytes decoded) or a package-private `staging` handle.
It MUST NOT raise frame limits. Staging MUST be App-owned 0600 under a private
0700 package directory. The App MUST verify size/hash and unlink after reading.
Header, execution, spool cleanup and error rules are specified in
[the raw contract](../../../docs/webapi-raw.md).

### Destination permission observations

`files.checkPermission` also supports an existing until-revoked
`files.moveFromStaging` destination: omit `job_id`, provide the exact granted
path and the current owner's session. The approved move policy permits only
this observation; it cannot widen the destination or grant new authority.
The executor rechecks current identity, App Privileges, scope, ACL and grant
fences. Legacy time-limited grants continue to require their `job_id` and
explicit operation policy. See `docs/dsm-session-transport.md` at the root.
