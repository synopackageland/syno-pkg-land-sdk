# Native daemon restart policy

`supervision` is the Go restart contract used by `daemonctl` and provider
supervisors. It runs as the package account, with no root service. Compose
containers retain their orchestrator's own restart policy; daemonctl never stacks
native child recovery on Compose authority/readiness supervision.

## Shared defaults

- Retry delay is uniformly sampled from 1 second through an exponential ceiling:
  2, 4, 8, 16, 32, 64, 128, 256, then 300 seconds. The 1-second floor prevents
  immediate restart loops while full jitter spreads launches after boot
- Eight unexpected exits within a rolling 10-minute window enter `failed`. With rapid
  crashes the first seven delays total at most 254 seconds, so the limit is
  reached before failures can expire from that window
- Backoff and failure history reset only after a continuous run of at least
  60 seconds and twice the previous delay ceiling. An individual successful
  probe or brief restart never resets them
- Resident children default to `always`: any unrequested exit (zero, nonzero,
  or signal) consumes the same retry budget. Requested disable, package stop and
  dependency teardown fence the policy with `Stop()` before terminating a child
- Tasks use `on-failure` (clean exit succeeds) or `off` (no recovery).
  Missing/unexecutable binaries, permission failures,
  cleanup failures and configured fatal exit codes fail without retry
- Terminal failure owns no retry timer. A user/config/package action can trigger
  another bounded series. `daemonctl` exits after persisting its terminal state;
  an explicit package start/restart re-arms it

The resident default follows [systemd Restart=always](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html#Restart=)
and [Erlang/OTP permanent children](https://www.erlang.org/doc/apps/stdlib/supervisor.html).
Explicit owner-requested stops do not restart; permanent failures remain terminal.

Policy itself owns no process, timer or goroutine. Adapters serialize lifecycle
actions and call it only after cleanup. `WatchNetwork` lets an existing enabled
host re-arm transient failed children on route/link/address or resolver events;
it blocks on kernel descriptors and has no polling timer. It never authorizes
or enables a disabled child.

## Native configuration

Native daemons use the defaults when `restart` is omitted. Runtime JSON accepts:

```json
{
  "restart": {
    "mode": "always",
    "initial_seconds": 2,
    "max_seconds": 300,
    "max_attempts": 8,
    "window_seconds": 600,
    "fatal_exit_codes": [78]
  },
  "dependencies": {
    "network": true,
    "dns": ["control.example.com"],
    "tcp": ["127.0.0.1:5000"]
  }
}
```

Declare only actual dependencies: local services need no external network.
`network` checks a default route; `dns` resolves the dependency's real names;
`tcp` checks local origins. Each probe is bounded, but total dependency waiting
has no timeout and consumes no failure attempts. Status shows `waiting`.
Initial child exits also enter the policy, including exits before the one-second
process stability check. Start admits the live supervisor while it waits or
backs off; status is successful only after the child stabilizes.

`mode: always` is the native daemon default, including omitted `restart`.
`mode: on-failure` preserves successful task completion on exit zero;
`mode: off` disables recovery. Zero numeric fields select defaults. Custom
delays accept 1–3600 seconds (maximum at least initial), attempts 1–100,
window 1–86400 seconds, and fatal exit codes 1–255. Unknown fields and invalid
values are refused before launch. Fatal codes must come from the child's own
contract; generic code 1 is not assumed permanent.

## Cleanup and load control

On Linux each foreground child owns a fresh process group with parent-death
signalling. The owner observes exit with `waitid(WNOWAIT)`, kills the remaining
group while its unreaped leader still pins the PGID, and then reaps and closes
its command descriptors. It never signals saved process inventory. Stop uses
a bounded TERM/KILL sequence. Cleanup failure blocks the next generation.
Non-Linux process helpers support host fixtures only; shipped daemons target Linux.

Two abstract Unix socket slots coordinate concurrent launches across package
accounts and independent supervisors on one Linux host. They need no writable
shared directory or root service and are released by the kernel after a crash.
The slot is held through startup stabilization, never during backoff or normal
process lifetime. This is load coordination, not an authorization boundary.
A process-local semaphore supplies the host-fixture fallback.

Package stop retains the existing durable fence and launch lock, lifetime lock,
private control socket, peer identity checks and config digest. Only an explicit
start under the command/owner locks clears the fence. A lost supervisor with a
witnessed live child remains cleanup-required; no automatic inventory takeover
or root work is introduced. Upgrade and uninstall still stop before replacement
or cleanup. Uninstall commands are not restarted.

## Diagnostics

`control status-json` exposes `state` (`starting`, `waiting`, `running`, `backoff`,
`failed`, `exited`, `stopped`, `unavailable`), `attempts`, `retry_at`, `last_exit`
(code/reason/permanent), the relative log pointer, restart count and existing
process/config inventory. A live snapshot authenticates the supervisor; terminal
saved state is diagnostic inventory, never readiness authority.

`control status` returns 0 only when ready, 3 when stopped and 1 while waiting,
starting, backing off or failed. Fixed diagnostics include
`DAEMON_WAITING_DEPENDENCY`, `DAEMON_RESTART_STARTING`, `DAEMON_RESTART_BACKOFF`,
`DAEMON_RESTART_LIMIT`, `DAEMON_PERMANENT_ERROR` and `DAEMON_CHILD_EXIT`.
The supervisor records a line for each state transition and captures the
foreground child's stdout and stderr. For every SDK-managed daemon, the
package's private `daemon/daemon.log` rotates at up to 4 MiB. It keeps one
previous file, `daemon/daemon.log.1`, for up to 8 MiB total across both files.
This leaves room for recent startup and restart diagnostics while bounding
storage per package. The next daemon start trims oversized existing logs to
their most recent 4 MiB. No App configuration is required.

Start with `daemon/daemon.log` when investigating a startup failure, and read
`daemon/daemon.log.1` for earlier output. To follow across rotation, use
`tail -F daemon/daemon.log`. The supervisor keeps draining the child's output
while disk writes are slow or failing. It drops output when its queue fills or
a write fails, and reports the number of dropped bytes in a
`DAEMON_LOG_DROPPED` line when it can write again. Use the status commands for
readiness and lifecycle state.

Apps may expose bounded fields through an authenticated API. They must not give
the browser direct process/log access. Runtime JSON dependency/fatal-code options
require a generator that supports them; default native recovery requires only an
SDK rebuild. Local fixtures establish policy/cleanup behavior, not DSM reboot
acceptance or Package Center refresh behavior.
