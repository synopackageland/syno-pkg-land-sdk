# Native daemon restart policy

`supervision` is the Go restart contract used by `daemonctl` and provider
supervisors. It runs as the package account, with no root service. Compose
containers retain their orchestrator's own restart policy; daemonctl never stacks
native child recovery on Compose authority/readiness supervision.

## Shared defaults

- Retry delay is uniformly sampled from 1 second through an exponential ceiling:
  2, 4, 8, 16, 32, 64, 128, 256, then 300 seconds. The 1-second floor prevents
  immediate restart loops while full jitter spreads launches after boot
- Eight failures within a rolling 10-minute window enter `failed`. With rapid
  crashes the first seven delays total at most 254 seconds, so the limit is
  reached before failures can expire from that window
- Backoff and failure history reset only after a continuous run of at least
  60 seconds and twice the previous delay ceiling. An individual successful
  probe or brief restart never resets them
- A clean exit stops. Missing/unexecutable binaries, permission failures,
  cleanup failures and configured fatal exit codes fail without retry
- Terminal failure owns no retry timer. A user/config/package action can trigger
  another bounded series. `daemonctl` exits after persisting its terminal state;
  an explicit package start/restart re-arms it

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
    "mode": "on-failure",
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
One bounded log line per state transition is written to private
`daemon/daemon.log`; raw child output is not a status API.

Apps may expose bounded fields through an authenticated API. They must not give
the browser direct process/log access. Runtime JSON dependency/fatal-code options
require a generator that supports them; default native recovery requires only an
SDK rebuild. Local fixtures establish policy/cleanup behavior, not DSM reboot
acceptance or Package Center refresh behavior.
