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
