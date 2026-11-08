# Runtime storage and NAS idle periods

An idle SDK runtime does not periodically rewrite its persistent state. This
covers the Go runtime used by Go Apps and by the Python, Node and Rust App
wrappers. An App's own workers, periodic commands and upstream services remain
responsible for their own disk activity.

| State | Location and write behavior |
| --- | --- |
| Background worker liveness | On DSM/Linux, a verified tmpfs at `/tmp`, `/run` or `/dev/shm`; a package-account-owned `0700` directory and atomically replaced `0600` observation. Only the latest observation is kept. |
| Jobs, inputs, schedules, checkpoints and receipts | The package-private persistent background spool. Changes retain the journal's file and directory synchronization. Idle polling does not rewrite the journal or touch its lock file. |
| Approved executor tasks | Root-protected `runtime/tasks.json`. Each check still reads both exact scheduler definitions. Only a changed or missing verified inventory is durably replaced. Failed verification retains the previous cleanup inventory and fails readiness. |
| Read-only delegated WebAPI audit | An in-memory ring of at most 256 events and 64 KiB of serialized JSONL. Overflow drops the oldest events without opening, appending or rotating the audit file. |
| Mutating and unclassified WebAPI audit | Synchronous durable JSONL, with one file synchronization per append batch. The directory is synchronized only on creation or rotation. Buffered reads precede the mutation in the same batch. |
| File read intermediate output | `files.read` retains the package-private persistent staging path and its existing transfer limit. Reading the source data volume already requires its disks. |
| Raw WebAPI intermediate output | Classified reads retain at most 1 MiB per active output in memory. Bodies above 32 KiB and at most 1 MiB may use verified private tmpfs next to the executor socket, bounded to 1 MiB and 256 unconsumed files per executor. Larger output and unavailable/full/failed tmpfs use the original persistent spool and its capacity checks. |
| Native identity bridges | Sealed memory descriptors, or a fixed set of unlinked read-only executable tmpfs descriptors prepared once at startup on older kernels. Account and privilege checks execute the bridge anew without restaging it to disk per request. |
| Policies, grants, revocations, root-job records and settings | Existing persistent protected/package state; synchronization and authorization rules remain in force. Real changes and active work may write. |
| Identity gateway and account bridge | Sessions, WebSocket revalidation and idle supervision stay in memory. Durable account/deletion records change on sign-in/account changes or pending deletion attempts. No pending deletion means no retry writes. |
| Logs | SDK loops do not emit successful idle tick/heartbeat logs or rotate files on a timer. Failures and application output can produce logs; active work and failure recovery are not idle periods. |

Worker liveness is keyed by the canonical background spool path and package UID,
so DSM's physical appdata path and `/var/packages` alias share one observation.
The package-account ownership, PID, worker revision, boot identity, process start
time and a 20-second freshness window are checked before reporting a live worker.
Normal worker shutdown clears the observation; an abrupt exit, PID reuse, reboot,
wrong revision, future timestamp or stale heartbeat cannot make an old worker
appear live. Heartbeat observations are not grants or job execution authority.
The delegate worker publishes once at startup and then from one five-second
heartbeat loop, independently of its job polling loop.

The executor classifies read-only WebAPI calls by intersecting an exact
API/method retrieval catalogue with the approved profile's API/method/version
allowlist. Classification does not depend on the App, profile name, purpose,
response type or a method-name prefix. Unknown calls and dispatch/batch wrappers
retain synchronous audit. For example, an approved Calendar event `list` is
buffered, while `create`, `delete` and an unknown API's `get` remain durable.
The maintained catalogue is in `executor/webapi_readonly.go`; mail message
`get` is deliberately unclassified because retrieval can change read state.

Pending read audit flushes only when all members of the system array are known
awake, before a mutating/durable audit event, or after callers drain on graceful
stop. A check at one-minute intervals reads only sysfs: the system array's
members, their Synology spindown/idle observations and rotation capability.
An unreadable, malformed or missing observation on a rotating/unknown member
means wait; a nonrotating member has no spinning disk to wake. The idle counter
is elapsed time since I/O, not a hibernation timeout; only a very recent
observation is accepted when no spindown indicator is available. Buffer
fullness and elapsed wall time never force a disk write.

On the next real flush, one ordinary audit event with operation `audit.dropped`
and result `dropped:N` precedes the retained events if overflow occurred. This
system marker is visible to administrators; ordinary events retain their
existing owner filtering and format. Readers see events after flush in
`audit.jsonl`, with the existing `audit.previous` rotation. A crash or power
loss can lose the last unflushed read events and their drop count. Mutating
audit, grants, revocations and operation records keep their durability rules.

Small staged raw responses identify transient output with `memory_staging`.
The Go client resolves it next to its trusted executor socket, checks the
private directory, consumes the file and removes it. Rebuild both the executor
and the App runtime together; a direct protocol consumer must support this
field. The protocol's maximum transfer size is unchanged. A body crossing the
1 MiB memory cap continues in persistent staging without repeating the DSM call.
If a small tmpfs handoff fails, the same buffered bytes use persistent staging.
The two shared output slots bound active body buffers; unconsumed tmpfs output
has a separate 1 MiB aggregate cap. Persistent staging or destination capacity
can still limit a transfer. A requested download or application-owned output
can read/write user data; buffering audit cannot suppress disk activity inside
DSM APIs or App code.

Scheduler readback still uses DSM's native API and checks both exact task
definitions on every check. DSM's EventScheduler initializer writes its SQLite
database even for read calls. Those calls run in a short-lived, private mount
namespace over a fresh, consistent SQLite backup in root-only tmpfs. Incidental
initialization writes stay in that snapshot; the host's scheduler database,
mounts and daemons are unchanged. This requires the existing DSM SQLite tool and
mount namespace support. A failed backup or isolation fails the readback; there
is no fallback that writes the host database or accepts cached approval.

The Linux runtime checks that its chosen filesystem is tmpfs and refuses to
publish a heartbeat when no safe private memory directory is available. It does
not use `TMPDIR` to choose a durable fallback. macOS development uses the system
temporary directory and process start time; the DSM idle-storage guarantee is for
Linux. Existing durable `worker.json` files from older SDKs are ignored; no
periodic migration or cleanup write is performed.

To adopt this behavior, rebuild the App's Go runtime (including the bridge
runtime for Python, Node and Rust Apps). Apps using an executor also need a
rebuilt, pinned executor and their normal administrator approval of the new
payload digest. No App-specific idle option or new shared service is required.
An npm-only frontend upgrade does not replace an already installed worker or
executor. User data, job history and settings need no migration.

On a kernel without `memfd_create`, native identity checks require a writable,
executable tmpfs. The fallback probes the configured private directory and the
fixed `/run`, `/dev/shm`, `/tmp` locations, checks tmpfs and the mount's execution
flag before creating anything, and caps each embedded payload at 1 MiB. If every
available memory mount is nonexecutable or unwritable, the checks fail closed;
there is no persistent executable fallback and no mount-setting change.
