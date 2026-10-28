# Protected executor

Standalone, standard-library-only Go module. Build with `CGO_ENABLED=0 GOOS=linux
GOARCH=amd64|arm64 go build -trimpath -o executor .`. No network listener: the
package account uses `/run/synopkgland/<package>/executor.sock` (one JSON line,
including newline at most 65536 bytes). Socket access additionally requires exact
Linux SO_PEERCRED UID equality, even for another member of the package group.
Non-Linux builds support portable tests and refuse privileged service operations.

Fixed writer children verify `no_new_privs` with `PR_GET_NO_NEW_PRIVS` on their
locked Linux thread. DSM kernel 4.4 supports this flag but omits its proc status
field; a failed syscall or a value other than one still refuses the write.
Saved IDs and zero capabilities remain mandatory. The parent passes the pinned
package-owned private source as a read-only FD; destination creation and publish
remain under the subject UID, and source cleanup runs under the package UID.

## Protected inventory and policy

Setup copies the INFO-anchored executor and policy-pinned policy, then runs
`executor install-root --package <package>` from the protected runtime to stage
the policy's root inventory into:

```
/usr/local/etc/synopkgland/<package>/             root:root 0700
  runtime/                                     root:root 0700
    executor                                   root:root 0700
    policy.json                                root:root 0600
    tasks.json                                 root:root 0600 (verified setup task inventory)
    root/<name>.sh                             root:root 0600 (script)
    root/<name>                                root:root 0700 (binary)
  delegations/                                 root:root 0700
    <random-256-bit-id>.json                    root:root 0600
    executor.pid                               root:root 0600
    executor.log                               root:root 0600 (control start)
```

`install-root` requires root and the fixed protected runtime. It validates every
source file's copied bytes against that entry's policy digest before publishing
any entry. Identical binary entries share a root-owned hard link inside this
App's runtime, while keeping their separate names and policy permissions.
Scripts and package-owned source files never share those inodes. A repeated
start or upgrade stages fresh inodes and renames them over the old names, so
updating one entry cannot overwrite another entry through an old hard link.
Every execution still opens, checks and hashes its own entry and executes that
verified descriptor; symlinks remain refused. Policy-unlisted source files are
not installed. A failed publish can leave a mix of old and new entries; each
execution still requires the exact approved digest, and a subsequent start
restages the inventory. Old policy-unlisted runtime files are retained until
purge, as before.

The runtime location is mandatory; `control purge` removes this package's entire
protected inventory after stopping it. The parent `/usr/local/etc/synopkgland`
may be root:root 0755. Ancestors must be root-owned and not group/world writable;
symlinks and writable runtime files are refused. Neither package-owned `etc` nor
package `var` is trusted for executor code or delegation storage. The chosen path
was measured on nas.example DSM 7.4; survival of future DSM updates remains unverified.

`policy.json` has this strict schema (no unknown keys):

```json
{
  "v": 1,
  "package": "com.example.downloader",
  "app": "com.example.downloader.Application",
  "scope": "home",
  "operations": [
    "status", "delegate.create", "delegate.list", "delegate.revoke",
    "files.list", "files.info", "files.createFolder",
    "files.checkPermission", "files.write", "root.run"
  ],
  "root": {"whoami": "<64 lowercase hexadecimal SHA-256 digits>"}
}
```

`app` is the actual registered DSM AppPriv application ID, not a guessed package
ID. DSM's native `SLIBAppPrivUserHas` decides it and File Station, through the
CGI's bridge embedded in the executor (`native/`, equal to `../dsm/native`): user,
group and everyone rules, deny precedence and IP ranges, evaluated for the
caller's `auth.remote_addr`, or for 127.0.0.1 when no browser asked (grants,
background operations, root's `delegate.setScope`). Administrators are asked
like everyone else. A 0, a failed bridge, or a caller without a usable address
refuses. Account eligibility is rechecked independently. `scope` is the
manifest's fixed `home` or `shared:<share-name>`; omit it for root-only packages.
`operations` includes every permitted ID, including management and status IDs;
no implicit allowlist exemptions. `root` is omitted when root operations are
disabled. Only names in this digest map can execute. `portal`
(`{"application": "<display name>", "alias": "<default alias>"}`) comes with
both `portal.register` and `portal.unregister` and is otherwise absent; the
package name must then be dot, dash or underscore separated letters and
digits of at most 64 bytes, since it names DSM's app.d and nginx files. Setup pins policy bytes and
all root scripts; no policy data comes from the package socket. The caller authentication pair follows the coordinator-approved v6 update to PLAN §2.4.

## Root programs

Each `root` entry pins one program. A bare digest is a script `root.run` runs
with the defaults; the object form declares the rest:

```json
"root": {
  "scan": {
    "digest": "<sha256>",
    "kind": "binary",
    "schema": {"type": "object", "additionalProperties": false,
               "properties": {"mode": {"type": "string", "enum": ["fast", "full"]}},
               "required": ["mode"]},
    "run": {"timeout_s": 60, "max_output": 67108864}
  }
}
```

- `kind`: `script` (`root/<name>.sh`, run by `/bin/sh`; the default) or
  `binary` (`root/<name>`, any executable the kernel runs, owner-executable).
- `schema`: the params a program accepts, a closed subset of JSON Schema:
  `type` (`object`, `array`, `string`, `integer`, `number`, `boolean`),
  `enum`, `pattern`, `minLength`, `maxLength`, `minimum`, `maximum`, `items`,
  `minItems`, `maxItems`, `properties`, `required` and `additionalProperties`,
  which every object must set to `false`. Keys are `[a-z0-9_]{1,64}`, nesting
  is at most 8 levels; any other keyword makes the policy invalid. Without a
  schema the program takes `{}` only.
- `run`: `root.run` may run it. `timeout_s` is 1–120 (default 20),
  `max_output` 1 B–64 MiB (default 1 MiB). An entry without `run` is refused by
  `root.run` with `POLICY_DENIED`.

`root.run` requires an administrator session whose Application Privileges
allow the App (`ADMIN_REQUIRED`, `APP_DENIED`); the `purge` entry is never
runnable. Params are checked against the schema before the caller
(`INVALID_REQUEST`), then written to the program's stdin as JSON followed by
EOF; nothing a caller sends reaches argv or the environment. The program gets
only `PATH=/usr/syno/bin:/usr/syno/sbin:/usr/bin:/bin`, `LANG=C`, `HOME=/`,
`SYNOPKGLAND_PACKAGE`, `SYNOPKGLAND_ROOT_MODE=run`, `SYNOPKGLAND_REQUESTED_BY`
(the verified user name) and `SYNOPKGLAND_REQUEST_ID`, runs in `/` in its own
process group without core dumps, and at most two run at a time
(`ROOT_BUSY`). The executor opens the program once without following
symlinks, beneath a symlink-free runtime, checks it is a root-owned regular
file that is not group or world writable and hashes it; the child then
executes that descriptor (`/proc/self/fd/3`), so a file swapped in after the
check is not what runs.

A non-zero exit is a result: the reply is `exit_code`, `stderr` (its first
16 KiB, as UTF-8) and either `output` (stdout, when it is UTF-8 and the reply
stays under 64 KiB) or `staging` and `size`: a `root-<random>` file in the
package staging folder, package-owned 0600, that the client reads and removes
like a `files.read` result. `staging` is the bare file name; the folder is
`/var/packages/<package>/var/executor-staging`, which the executor creates
(package-owned, 0700) when the App has not. A staging folder the executor
cannot use is `ROOT_FAILED` with the message `root output staging
unavailable`, and executor.log names the program and the cause. Staged root output may total 256 MiB
(`ROOT_STAGING_FULL`); a file no client took within an hour is removed. Output
past `max_output` kills the program (`limit-exceeded`), the timeout kills its
process group (`ROOT_TIMEOUT`), and a signal or a start failure is
`ROOT_FAILED`. The request may carry up to 1 MiB of params and the executor
answers within 130 seconds; clients wait 140.

`root.run` cannot be canceled. A client that stops waiting (a browser that
disconnects, or a bridge request whose deadline passes, so the runtime
stops waiting) only loses the answer: the program runs until it exits or
its `timeout_s` kills it, and staged output nobody took is removed after an
hour. Work that a person may want to stop, or that runs longer than a
request, is a job: `root.start`, then `root.jobs.stop`.

### Root jobs

An entry with `job` (`timeout_s` 1–604800, default 86400; `grace_s` 1–60,
default 10; `max_concurrent` 1–4, default 1; `nice` 0–19, default 10) runs in
the background through `root.start`, and `root.jobs.list`, `root.jobs.get`
and `root.jobs.stop` follow it (protocol: Root jobs); the four come together
in `operations`, and a policy with a `job` entry needs them. The caller checks
are those of `root.run`. The package runs at most four jobs. A job runs like
a `root.run` program with `SYNOPKGLAND_ROOT_MODE=job`, its `SYNOPKGLAND_JOB_ID`,
its nice value, a cancel pipe on fd 4 and a progress pipe on fd 5; the first
4 KiB of stdout and stderr become its `output`. Its record,
`delegations/rootjob-<id>.record` (root:root 0600), holds its state, latest
progress, result and, while it runs, its process group and that process's
start time; progress reaches it at most once a second and at the end. The
64 newest records are kept.

Jobs belong to the open service: a package stop cancels them with `stop`, an
uninstall with `purge` and any other executor end with `executor-stop`, each
killed at most 15 seconds after its cancel, before the socket goes away. A
record an executor left `running` or `stopping` becomes `interrupted` at the
next start, and its process group is killed when `/proc/<pid>/stat` still
shows the recorded start time, so a reused pid is never signaled.

### Package stop step

A policy with `"stop": "<name>"` runs that entry as root after the jobs of a
package stop ended and before the socket goes away, with `stop`, or with
`prepare-upgrade` when the package account wrote `upgrade` to
`var/executor/stop-reason` (`executor/lifecycle stop --upgrade`, from DSM's
`preupgrade`; a plain `stop` writes `stop`). An upgrade cancels the jobs with
`upgrade`. Like the purge step it runs only while the approval is live, for at
most 30 seconds (`SYNOPKGLAND_ROOT_MODE=stop`), logs
`stop root step: ... exit=... output=...` and never blocks the stop;
`root.run` and `root.start` refuse it, so the policy is invalid when the stop
or purge entry declares `run` or `job`; pin the same digest under another name
for the session's calls. An executor that ends for another reason runs no
stop step.

### Package start step

A policy with `"start": "<name>"` runs that entry as root once after each
install or upgrade: at the first start of a package version (INFO
`version`), with no session. The socket opens first; the step runs once the
executor has read the approval tasks back, and until it ends `status`
answers `ready: false, state: "starting"` and every other request
`EXECUTOR_STARTING`. Its arguments are
`install`, or `upgrade` and the version that started before. It runs only
while the approval is live, for at most 60 seconds
(`SYNOPKGLAND_ROOT_MODE=start`), logs `start root step: ... exit=...
output=...` and never blocks the start: the executor becomes ready whatever it answers.
The executor records the version it started for, with the step's result, in
`start.record` of its protected state; `status` reports that result as
`start_step`. A step skipped without approval, or cut short because the
executor ended, runs at the next start; a failed one stays the result of
that version. Like stop and purge, the entry cannot declare `run` or `job`.

### Root state directory

With `"root_state": true` the executor resolves `/var/packages/<package>/var`
at start; DSM keeps it as `/volumeN/@appdata/<package>`, and the directory is
`/volumeN/@synopkgland/<package>`, root:root 0700 below a root:root 0755
`@synopkgland`, created through root-owned, not group or world writable,
symlink-free components. Every root program, lifecycle steps included, gets
it as `SYNOPKGLAND_ROOT_STATE`. When the package data is anywhere else, or
the directory fails those checks, `root state unavailable` is logged and the
policy's `root.run` and `root.start` are `ROOT_FAILED`. Package upgrades do
not touch it. A live purge removes it after the purge step for
`delete-state` (`root state removed`) and keeps it for `keep-state`; an
offline purge leaves it.

Assumptions not yet verified on a NAS: DSM never creates, scans or removes
`@synopkgland` on a volume; volume roots are root-owned and not group or world
writable; `/var/packages/<package>/var` links to `@appdata` on DSM 7.x; and a
reinstall on another volume starts with an empty directory, the old one
remaining until an administrator removes it. Starts,
stop requests and ends are audit events (`root.start`, `root.jobs.stop`,
`root.job`).

## Requests

Every request: `{"v":1,"op":"...","auth":{},"params":{...}}` followed by
newline. `auth.dsm_cookie` is only the DSM `id` value, never a raw Cookie header.
Caller requests also require `auth.dsm_synotoken`, forwarded from the current
HTTP request's X-SYNO-TOKEN or SynoToken header. Both aliases may be present
when their values match; conflicting aliases or repeated values of either header
are refused. `auth.remote_addr` is the browser's address (REMOTE_ADDR) for the
session verification and App Privileges check. DSM's native `authenticate.cgi`
receives that original address, the id cookie and X-SYNO-TOKEN (also as its
URL-encoded CGI query); neither credential enters argv, logs or storage. It
never replays a browser session over loopback HTTP, which invalidates IP-bound
DSM sessions (`skip_ip_checking=no`, observed on DSM 7.1.1). A missing/invalid
address or token refuses authentication; only the native result establishes identity. Other
identity fields, duplicate JSON keys, invalid UTF-8, unrelated parameters, unknown
operations and policy-excluded operations are rejected. Replies follow §2.4.

| Operation | Parameters | Result |
| --- | --- | --- |
| status | none | executor_digest, policy_digest, version, ready, disabled, tasks (the executor's own task readback: state, checked_at) |
| delegate.create | job_id, ops[], scope, path, ttl_seconds | delegation_id, subject {username, uid}, expires_at (RFC3339) |
| delegate.list | none | delegations[] with public metadata, including revoked/expired records |
| delegate.revoke | delegation_id; job_id required without cookie | revoked |
| files.list, files.info, files.checkPermission | delegation_id, job_id, path | DSM data object |
| files.createFolder | delegation_id, job_id, path (parent), name, optional parents | DSM data object |
| files.write | delegation_id, job_id, staging (relative), dest_dir, name, optional conflict | path |
| files.read | delegation_id, job_id, path, optional max_bytes (at most 64 MiB) | staging, size |
| files.rename | delegation_id, job_id, path, name | DSM data object |
| files.copy, files.move | delegation_id, job_id, path, dest_dir, optional conflict (fail, overwrite) | task_id, state |
| files.delete | delegation_id, job_id, path, optional recursive, optional permanent | task_id, state |
| files.taskStatus, files.taskCancel | delegation_id, job_id, task_id | task_id, state, progress, error |

Calls made as the delegated user go through one allowlist (`userCalls` in
`dsm.go`): File Station List list/getinfo, CreateFolder create, CheckPermission
write, Rename rename, CopyMove and Delete start/status/stop. Anything else is
refused before `synowebapi` runs. Paths File Station reports back are checked
against the grant again. Copy, move and delete are File Station background
tasks; the executor keeps at most 64 task records in memory, and a task DSM no
longer knows (error 599) reports state `unknown`. `files.read` runs a fixed
`read-child` that drops to the subject, opens the file beneath the grant without
following symlinks and copies it into a new root-created, package-owned 0600
staging file.
| root.run | name, params | request_id, exit_code, stderr, output or staging and size (Root programs) |
| root.start | name, params | job_id, state (Root jobs) |
| root.jobs.list | none | job summaries (no result, output, message or phase), newest first |
| root.jobs.get, root.jobs.stop | job_id | the job |
| portal.register, portal.unregister | none | entry (`registered`, `unregistered`, `cleanup-required`) |

Registration rollback gets its own two-minute deadline, even if registration
exhausted its deadline. A failed rollback keeps the cleanup-required marker.
Failures log only the package, fixed step and reason (`timeout`, `canceled`,
`dsm-N` or `command`), never command output or authentication material.

`portal.register` and `portal.unregister` require an administrator session the
App's Application Privileges allow, like `root.run`. They act on the fixed
DSM paths of protocol v1's Application Portal section; the app.d json and
mustache are generated from the policy, never read from the package. Each
change runs within two minutes, after the reply's 30 seconds too, and only one
runs at a time. A failure, or an executor that stopped during a change, leaves
the root-only `delegations/portal-cleanup` marker; `status` reports
`cleanup-required` until a `portal.unregister` succeeds. A running change holds
the `delegations/portal-change` lock, and its marker reads as `registered`
until the change ends.

Create/list require a cookie and SynoToken. Revoke accepts the subject/admin session pair or a matching
package job ID. Root operations require the session pair independently verified as an
active administrators member. Delegated operations recheck the account, UID,
expired=`normal`, App and File Station privileges, scope,
job, operation, expiry and revocation. UIDs below 1024 are excluded as system
accounts. TTL must be 1–2592000 seconds. No cookie/SynoToken/SID/password is persisted.

Paths use File Station spellings `/home[/...]` or `/<share>[/...]`. A delegation
binds the requested directory's canonical path. Symlinks resolving outside that
directory are denied. `files.write` opens package-owned, singly linked regular
staging files with no symlink components; its fresh child checks setgroups,
setresgid and setresuid in order, then real/effective/saved/filesystem IDs and groups, empty capability sets and
`no_new_privs`. Admin users retain their own nonzero UID and current groups. The child
opens the destination after dropping identity, verifies its pinned directory and
scope, and copies into an exclusive hidden `.synopkgland-<job_id>-<random>.part`
file. After fsync it publishes the complete file with a hard link, then removes
the temporary name. Existing files and symlinks are never overwritten. The
executor cleans the exact temporary name as the subject user after a canceled or
killed child, using a retained directory descriptor; startup performs no scan.
Successful writes request exact source cleanup in a separate package-UID child;
cleanup failure retains the source and never downgrades a committed receipt. DSM ACL inheritance can
produce a mode different from the requested 0600 (observed 0711 on nas.example).

File Station CheckPermission uses v3 with a fixed non-overwriting probe filename
and size zero; it creates no file. Missing/failed API data is denied. DSM output
is capped at 1 MiB; commands have 20-second deadlines,
connection read/write deadlines are 15/5 seconds and request execution is bounded
by 30 seconds. At most 16 connections are admitted. Legacy runner APIs use short
admission leases; revoke/disable persist the fence then drain accepted legacy
calls outside the commit lock before acknowledging. Bulk copy holds no commit
lock. Linux command timeouts kill the entire command process group.

File Station APIs resolve path strings inside DSM after executor scope validation;
there is no atomic DSM API for binding that subsequent lookup to our directory
FD. Concurrent subject-controlled directory renames remain a scope-race limitation
for runner API operations. The writer uses pinned directory descriptors; a user
can still move a directory after its final scope check. These are release review
items, not assertions of complete race freedom.

## Lifecycle and checks

```
executor serve --package com.example.downloader
executor supervise --package com.example.downloader
executor control start --package com.example.downloader
executor control stop --package com.example.downloader
executor control purge --package com.example.downloader
executor check-policy --package com.example.downloader < policy.json
```

`check-policy` decodes and validates a policy exactly as `load()` does before
`serve`, with no root and no runtime directory: exit 0 when the executor would
accept it, otherwise exit 1 with `executor: invalid policy: <reason>` on stderr.
A build can run it so a policy the executor would refuse never reaches approval.

Root control start launches asynchronously; the caller reads socket status to
establish readiness. It starts `executor supervise`, a root supervisor that runs
`serve` as its child and restarts it after an abnormal end (signal or non-zero
exit) with a 1 s backoff doubling to 60 s, reset after a minute of healthy run.
A restarted `serve --revived` honors a `stop` already in the mailbox. Exit 0
(root replacement, shutdown, purge), exit 3 (another monitor holds the pidfile)
or a removed runtime end supervision. A held flock pidfile refuses duplicate
serve. Root's pinned
boot/control start can validate and terminate a previous monitor before launching
its replacement; package-account start only writes the mailbox and cannot replace
runtime. Stop pauses the listener while the monitor continues. Purge removes this
package's protected state and deletes only its verified recorded scheduler tasks.

Run `go test -race ./...` and `bash scripts/ci/gate.sh native-go`. The gate adds
Linux amd64/arm64 vet, static binary builds and test compilation. Linux-specific
syscall tests require a disposable Linux root fixture. This revision was tested
on a Linux VM; DSM ACL, logout and reboot behavior remain unverified. The
`testdata/live_probe.py` harness expects isolated disposable accounts and a
manually copied runtime, reads passwords only from stdin, and logs no credentials.
The live harness uses DSM's registered `SYNO.Desktop` AppPriv ID as a stand-in for
an installed App; installed-package AppPriv integration belongs to setup testing.


## Package lifecycle

Package-account hooks atomically write `stop` before upgrades/stops, `start` on
install/upgrade completion and package start, and `purge` before uninstall.
`stop` cancels/drains requests and removes the listener without ending the root
monitor. `start` resumes if both package executor and policy hashes equal the approved
runtime pins. If only the executor changed, the policy hash is unchanged and the
executor hash equals the single `synopkgland_executor_sha256` key in the
root-owned `/var/packages/<package>/INFO`, the root monitor copies it into
`runtime/`, verifies the copy and runs root `control start`, which replaces the
monitor without reapproval. Any other change stays paused for administrator
approval. Only root's pinned boot/control path or this anchored install can
replace a monitor or runtime. To exercise this path on a NAS without a real
executor change, the repository's `scripts/test/executor-bump.py` makes a
build+1 SPK whose executor differs by one byte and rewrites the INFO anchor,
`executor/setup.json` and the window script's embedded pins together.

At start, scheduler readback must exactly match the canonical lifecycle scripts
regenerated from the package identity and approved policy pin. Package
setup metadata supplies hints only. The boot task is got by name (DSM keys
EventScheduler tasks by name); the control task is listed for its id. A root
step's approval check gets that listed id again instead of listing; purge
always lists. The rules and their shared test vectors are
`spec/scheduler/v1`. The verified names, pins and script hashes
are stored in root:root 0600 `runtime/tasks.json`. Purge regenerates the scripts
and revalidates each current definition before EventScheduler v1
delete (`task_name`) or TaskScheduler v2 delete (`tasks=[{id,real_owner}]`).
Without `tasks.json` (an install whose setup.json the snapshot refused, as
CLI 0.9.0's with `sdk` was before SDK 0.9.1), purge rebuilds the same
inventory from the root-owned `runtime/policy.json`. Purge also deletes the
install-selected `<pkg> executor selection` TaskScheduler tasks whose script
has the exact selection shape for this package.

DSM keeps two things after any uninstall, so purge also removes them, logging
each failure without stopping the rest of the cleanup:
- Application Privileges rules of exactly `SYNO.SDS.<pkg>.Instance`, listed
  and deleted with `SYNO.Core.AppPriv.Rule` v1 (`list app_id`, `delete
  rules=[{app_id,entity_type,entity_name}]`), the calls Control Panel makes.
  Rules of any other App id are never sent. DSM runs every package's
  `app_privilege_set` hooks inside the delete (16-20 seconds on nas.example), so
  it gets 90 seconds instead of the 20 every other DSM command gets (the
  per-call bound is capped at 3 minutes). The rules listed after the delete
  decide the outcome, also when the delete call failed; the journal line
  carries `delete_error=timeout|canceled|dsm-<code>|command` then.
- The Login Portal alias. DSM's web-config worker removes the app.d and nginx
  files when it stops the package, before preuninst, but does not reload
  nginx, so the alias keeps answering. When `conf/resource` declares
  `web-config` `app-portal` and none of `app.d/<pkg>.json`,
  `conf.d/{,.}alias.syno-app-portal.<pkg>.conf` or
  `conf.d/dsm.syno-app-portal.<pkg>.conf` remains, purge runs
  `synosystemctl reload nginx` (nginx.service's own reload). After the
  10-minute missing-package fallback the declaration is gone, and the reload
  is skipped and logged.
- The executor's own Application Portal entry. When the retained
  `runtime/policy.json` has `portal`, purge runs `portal.unregister`'s steps
  (app.d files, `AppPortal.json`, every generated conf and site, `nginx -t`,
  reload) and logs `portal entry cleanup` or `portal entry cleanup failed`.
  DSM removes none of it on uninstall (the regression test, DSM 7.1). An unreadable policy
  skips the step and logs it; a policy without `portal` leaves every portal
  file to DSM.
Before any of that, a policy with `purge` runs that root op, the App's own
uninstall step (protocol: Uninstall purge step). It runs only from the live
monitor's purge, while the package ships the approved pins and both approval
tasks of that policy stand unchanged; `root.run` refuses it. Its argument is
`delete-state` when the package account wrote that answer to
`var/executor/uninstall` (`executor/lifecycle purge --delete-state`; otherwise
`keep-state`). It runs for at most 60 seconds, and the log line carries its
exit (`0`, the code, `timeout`, `signal` or `refused`) and up to 4 KiB of
output. Without a live approval, and on every offline purge, it is skipped and
`purge root step skipped: not approved` is logged.
Boot mirrors from TaskScheduler are ignored. Changed, missing, ambiguous or
unverifiable records are logged and retained for administrator review; protected
runtime, delegations and sockets are still removed, then the monitor exits.
An absent process does not block uninstall; the pinned boot task handles pending
purge or a missing package on the next boot. If the retained binary/policy is
missing or tampered with, boot refuses execution and requires manual review.


The separate package-account `executor/lifecycle` helper writes an atomic 0600
mailbox request. Stop waits at most 50 seconds for socket disappearance and
purge 110 (the App root step may take 60 of them);
start returns after writing and lets the monitor verify pins asynchronously.
The helper never calls synowebapi or TaskScheduler. Socket inspection failures
and timeouts warn and permit lifecycle to continue. Write/validation failures
remain errors. Delegate hooks attempt worker stop first; a worker stop failure
warns and still requests executor pause/purge.

The monitor polls every 500 ms with bounded regular-file reads, accepting only
exact start/stop/purge (optionally one trailing newline). Invalid values are
ignored and logged without logging contents. The preuninst `purge` mailbox is
handled immediately; cleanup completes asynchronously. Package directory/INFO
identity absence is a fallback: the monitor pauses the socket and requests,
retains runtime/delegations/tasks, and purges only after 10 continuous minutes of
confirmed absence, even while paused. Every poll checks again; a valid identity
resets the timer, and an unreadable path cannot count as confirmed absence.
Reappearance re-reads the mailbox, including an unchanged `start`: matching pins
resume, changed pins stay paused for administrator approval, `stop` stays paused,
and `purge` clears state immediately. A root-approved new monitor ignores the
stop file already present at startup; that exception ends after an absence.

The boot task checks pending purge/package disappearance before accessing source.
It verifies the retained policy pin before offline purge from the root-only
runtime. Normal boot verifies the copies against the policy pin and the INFO
executor anchor, installs them and calls root control start to replace a
previous monitor. Scripts stay within 1 KiB with no percent characters, including the
longest package name. Partial/tampered retained state requires administrator
review. All nine uninstall wizard locales name the two tasks and disclose the
retained DSM storage/infrastructure. `inspect`/`status` cleanup guidance is
conditional; it does not claim that remote remnants were observed.

Directories newly created by the executor are explicitly chowned/chmodded through
a directory descriptor and verified with fstat, so boot umask 0077 cannot reduce
/run/synopkgland 0755 or its package directory 0750. Existing directories are
validated and never repaired/adopted. The package directory must already have
root/package-group ownership. The socket is set to root/package-group 0660 and
verified using Linux O_PATH + fstat on its filesystem inode.

## Explicit long-running v1 extension

The pinned policy opts in with top-level `destinations: ["save"]` and
`lifetime: "until-revoked"`, together with `files.moveFromStaging` in the
operation allowlist. Destination keys match `^[a-z][a-z0-9_-]{0,63}$`; unknown
keys/lifetimes fail closed. Legacy policies/TTL grants never become permanent.
The existing v1 envelope, peer UID check, protected runtime and session verifier
remain unchanged. `root.run` is independent and is never a writer fallback.

| Operation | Parameters | Result |
| --- | --- | --- |
| delegate.create (permanent) | scope, ops:[files.moveFromStaging], path, destination, lifetime:until-revoked | delegation_id, subject, path, destination, lifetime; zero expiry |
| files.moveFromStaging | delegation_id, job_id, staging, name (may contain up to 16 folders), size, optional conflict (fail/overwrite/rename) | job_id, state |
| files.operationStatus | original delegation_id, job_id | job_id, state, optional code/receipt |
| files.cancelOperation | original delegation_id, job_id | current operation state |
| delegate.disable / delegate.enable | none; live administrator session | disabled |
| delegate.audit | none; live owner/admin session | latest bounded events |

Permanent create needs the current user's cookie and SynoToken and a successful
File Station write check. The executor derives username/UID and exact directory
identity itself. Permanent revoke is owner-bound even for administrators; admins
can disable the entire package. Auth-less operation submission/status/cancel is
for the trusted package producer holding the grant reference and original job ID.
If credentials are supplied, both must verify and the owner must match; failed
caller auth never falls back to offline authority. A compromised package backend
can use all valid grants it holds, as documented in the approved trust model.

Grants survive service restart in the protected store. The disabled flag also
survives; mailbox start/boot never clears it. Disable persists first and revokes
all legacy/permanent grants. Enable checks the live admin session and completes
revocation before clearing the flag; it never restores old grants. Repeated enable
while already enabled is a no-op.

Operations are separate `operation-<sha256(job_id)>.record` files, root:root 0600.
They bind immutable grant/job/staging/name/size and move through queued, copying,
committed, canceled, interrupted or uncertain. Queued resumes without credentials;
copying becomes interrupted on restart, or uncertain after durable publish intent.
Neither state is replayed. Changed input for the same job is `operation-conflict`.
Committed status alone includes `receipt:{path,size}`. Cleanup failures retain
committed and add `CLEANUP_PENDING`. No same-name/hash guessing establishes receipt.

Limits: 16 GiB per file, actual size+1 read, 16 queued, one active copy per package,
24-hour attempt, 10 MiB operation metadata. Full journals reject new admissions;
terminal records are retained to prevent replay. Bulk input bypasses job spools
and the old 64 MiB wrapper limit. Long copies revalidate account/AppPriv/ACL/path
with five-second polling and bounded checks; publish revalidates again. An ACL
denial (`PERMISSION_DENIED`) or a granted folder missing from its path
(`not-found`) fails that operation and keeps the grant, so a new job succeeds
once the cause is fixed. Any other failed account/AppPriv/scope/identity check
revokes the grant and requires a new selection.

The fixed child drops identity before opening the destination and creating any
inode. Every component uses FD-relative O_NOFOLLOW; ancestors use O_PATH so only
search rights are required. Source is package-owned, regular and singly linked;
destination is the exact granted device/inode, never a caller-supplied path.
Part metadata is durable before copying. Publish links the open part inode with
no replacement, refusing files/symlinks/FIFOs at the final name. Folders in the
name are opened one at a time with O_NOFOLLOW from the granted directory and
created by the child as the subject after the final fence; a symlink, file or
other device there is `destination-not-directory`. `conflict: rename` links
`name (n).ext` (n ≤ 99); `conflict: overwrite` links a temporary in the same
folder and renames it over a regular file the subject can open for writing. Revoke, disable
and publish share the commit lock. Lost/malformed publish replies drain the child
before releasing that lock and produce uncertain; a confirmed no-side-effect
refusal such as `destination-exists` remains interrupted. The lock covers bounded
publish handshake/fsync, never the bulk copy. During the copy the child watches
its control pipe: if `serve` dies the pipe closes, so the child stops within one
1 MiB chunk and unlinks its part (no `PR_SET_PDEATHSIG`, which the identity drop
clears; works on kernel 3.10). Kernel-blocked filesystem I/O can
still delay draining; it cannot be declared safely complete by a timeout.
Legacy `files.write` uses this same child and publish handshake; the executor
has no other writer, so no child can publish after `serve` is gone.

Destination/part cleanup runs as the owner; source cleanup runs as package UID.
Restart cleans only journaled exact part identity as the current eligible owner;
changed/missing/inaccessible directories are retained for review, never scanned
or deleted as root. Subject-controlled directory moves after the final check
can still complete in the pinned directory object. DSM ACL changes and publication
are not atomic, and silent UID/inode reuse is not universally detected. A subject
who owns the destination can mutate its own output; this is not a content sandbox.

Structured audit lives in protected `audit.jsonl`/`audit.previous`, at most 50 MiB
combined, separate from authority. Events include package, username/UID, operation,
job, location and result; no session credentials. Owner queries are filtered,
admins can view this package's events. Replies contain at most 256 events/1 MiB.
Admission events are labeled `admitting` because a subsequent durable write can
fail. The operation journal, not an admission audit event, establishes completion.

The existing status operation reads `Store.Disabled`; protected-store errors set
`ready:false` rather than assuming enabled. Backend `delegate.status` exposes the
verified boolean and `disabled` capability state. Backend `delegate.operations.list`
is strictly empty-params, freshly owner/UID-filtered move-mirror metadata; it is
not an executor operation. Live completion remains `files.operationStatus`.

## Delegated JSON WebAPI profiles (0.11.0)

A policy may declare `webapi` alongside its other capabilities:

```json
{"calendar":{"purpose":"Create calendar reminders after workflow approval","apis":{"SYNO.Cal.Cal":{"list":[5]},"SYNO.Cal.Event":{"list":[6],"get":[6],"create":[6],"delete":[6]}}}}
```

Add `webapi.call`, `delegate.create/list/revoke`, and the desired
`delegate.disable/enable/audit` operations. Profiles are included in the policy
pin and existing administrator approval; changing any profile requires approval
again. Each user consents with `delegate.create` using `ops:["webapi.call"]`,
`scope:"calendar"`, and either `job_id` + `ttl_seconds` (at most 30 days) or
`lifetime:"until-revoked"`. Omit `path` and `destination`; file grants and WebAPI
grants cannot be mixed. The grant pins its profile definition. Its owner may
revoke it; the administrator's integration switch also fences all WebAPI calls.
A retry of an unchanged until-revoked consent returns that user's active grant.

`webapi.call` takes `delegation_id`, `job_id` for a timed grant, `api`, `method`,
integer `version`, and object `params`. The executor checks current eligibility,
AppPriv with the stored original address (127.0.0.1 fallback), lifetime, revocation
and profile pin on every call. A call carrying live auth must belong to that
same user. It then runs the fixed synowebapi executable with `runner=<owner>`;
no password or session is stored. File Station privileges are independent.

Authentication, system/Core, File Station, account, group, permission, owner,
sharing, package and administration namespaces, API/Entry dispatch indirection,
and authentication/permission methods are permanently refused. Reserved routing,
authentication and runner params cannot be supplied. Exact API/method/version
matches only; no wildcard profiles. Maximum: 32 profiles, 256 version entries,
1024-byte purpose, 32 KiB encoded params, 48 KiB JSON reply, and a 10-second call
bound (including revalidation). Invalid/duplicate JSON is refused. The normal
64 KiB wire limit also applies. Audit contains identity, grant, profile, API,
method, version and admission/completion; it never records API params/results.
Revocation/disable persist their fence and drain previously admitted calls.

The result is the **full DSM envelope**: protocol `ok:true` can contain DSM
`success:false`. `WEBAPI_UNCERTAIN` means execution or a reply/audit failed after
admission; a transport failure can also hide a completed mutation. Never retry
mutations automatically. Reconcile through the App's own operation journal and
an approved read API, or offer an explicit manual recovery action. Binary,
streaming, compound and session-dependent APIs are outside this operation.

DSM 7.3.2 probes on 2026-10-16 verified Calendar v5/v6 (including cross-user
9007 denials), MailPlus Mailbox v7/Thread v10/Message v10, and Drive Files list v2 /
get v3 (cross-user 1002). Office Snapshot/Export enforce permissions but return
streaming output from synowebapi and are excluded from this JSON operation.
These measurements do not certify every package API: validate each profile
against its target DSM/package version before enabling it.

## Raw WebAPI profiles (0.12.1)

Profiles may opt in with `raw_max_bytes` (1..67108864); zero/omitted stays JSON-only.
`webapi.call` then accepts `response_type:"raw"`. Small bodies use base64; large
bodies use App-private staging with size and SHA-256. See
[contract, security bounds and Go consumer example](../docs/webapi-raw.md).
Attachment.download and X-Accel-Redirect are excluded.

### Request uploads

Profiles may opt in with `upload_max_bytes` (1..96000); zero/omitted keeps the
profile and its digest unchanged. `webapi.call` then accepts
`upload:{param,staging,size,sha256}`: the executor reads the App-staged file,
verifies it, unlinks it and sends it as one base64 `param`. See
[the upload contract](../docs/webapi-upload.md).

### Native account eligibility

Every request reads a pinned, sealed-memfd bridge (unlinked root-only fallback
on old kernels). libc NSS binds the exact username to its UID and resolves the
UID back to the same canonical name (AD display casing is checked through
a second forward lookup); a final lookup after expiry and group queries
rejects rename/removal/UID replacement. Both native expiry primitives must
explicitly say normal (`day == -1` and `CheckExpired == 0`). Administrator
membership uses DSM's native check with its directory cache bypassed.

Local identity is established from the root-owned local passwd database,
matching both name and UID. Other identities must also resolve through `wbcGetpwnam` with the same
name/UID/GID, proving the AD provider, and require a successful `wbcPingDc` for the NSS canonical account's domain before reading expiry;
cached NSS data alone cannot admit an offline directory. The library and
provider must be available. LDAP and noncanonical directory identities without
a verified AD provider refuse; LDAP has not been tested. On 32-bit runtimes,
UID/GID values that do not fit the existing signed `int` identity contract
refuse rather than truncate. This limitation is not changed by the expiry fix.

This fixes the prior accidental rejection of directory accounts based on the
missing Core.User `expired` field. App Privileges, admin-only/root-op rules,
`authenticate.cgi`, and all file/operation checks remain authoritative. Native
account and privilege results are never cached by the executor.
