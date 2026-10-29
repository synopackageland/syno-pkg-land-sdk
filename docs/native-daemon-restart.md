# Native daemon restart policy

`daemonctl` can restart its directly owned native foreground child after a
nonzero exit or signal termination. Native daemons default to **on-failure**
with bounded exponential backoff, without any App configuration. Compose/network
supervision keeps its own restart policy. Recovery can repeat upstream work, so
foreground daemons must tolerate being started again. The supervisor never changes
UID, invokes a root service, or signals a PID from saved inventory.

## Configuration and defaults

The SDK accepts this optional object in the generated `daemon/config.json`:

```json
{
  "restart": {
    "mode": "on-failure",
    "initial_seconds": 1,
    "max_seconds": 30,
    "max_attempts": 5,
    "window_seconds": 300
  }
}
```

Omit `restart` to use these defaults for native daemons. Set `mode: off` to
disable automatic restarts. `mode` is required when the object is present. Omitted or zero numeric fields use the
defaults above. Unknown fields, fractional numbers, unsupported modes and
out-of-range limits are rejected before starting a child. Delays accept 1–3600
seconds, maximum delay must be at least the initial delay, attempts accept
1–100, and the sliding window accepts 1–86400 seconds.

After an unexpected failure, delays double up to `max_seconds`. With the defaults,
a rapid crash sequence waits 1, 2, 4, 8 and 16 seconds before exhausting its five
automatic attempts. Each automatic attempt is reserved when scheduled; at
most `max_attempts` reservations may fall in the preceding `window_seconds`.
The initial manual launch does not count. Expired reservations leave the window;
the delay depends on the remaining reservations. A full window terminates the
supervisor with `DAEMON_RESTART_LIMIT`; it does **not** wait for a window reset
and start again. Only an explicit package start/restart begins a new budget.
A successful recovery does not clear the window, so a child that keeps failing
soon after recovering still reaches the limit. Supervisor lines in `daemon.log`
start with the UTC time; `attempt=` counts the reservations in the window and
`restarts=` all automatic restarts of this supervisor.

A child must survive the existing one-second process stability check before it
is reported running again. Exits during this recovery check and exec failures
consume the same bounded budget. Exit code zero never restarts. A signal that
the child handles by exiting zero is also clean. An externally unhandled signal
counts as failure, including SIGKILL; this is deliberately narrower than
systemd's special treatment of several unhandled signals as clean exits.

Initial package startup retains its original deadline and fail-fast behaviour;
this policy applies only after the first successful startup. It does not mask a
failed installation or extend the caller's startup deadline. Compose/network
supervision rejects an enabled policy: container restart belongs to its existing
orchestrator, and SDK readiness/authority failures must not be retried by this
native-child policy.

## Stop, upgrade and uninstall

Package stop writes and syncs a package-private stop fence before asking the
supervisor to stop. The fence and automatic launch share a lock, so a launch
cannot pass a completed stop fence. An explicit start clears it only while
holding the command and supervisor ownership locks. Stop during backoff or the
recovery stability check cancels the pending attempt. Supervisor SIGTERM/SIGINT
also stop without recovery. Uninstall uses the same stop path before running its
separately bounded cleanup command; upgrades must keep using the existing
stop-before-replacement lifecycle. Uninstall commands are never restarted.

The supervisor keeps its lifetime lock and private control socket across child
generations. A lost supervisor is still cleanup-required when a witnessed child
remains; there is no inventory-based kill or automatic takeover. Failure to
write state or acquire a required lock fails closed.

## Package Center and App diagnostics

The existing `control status <target> <var>` command remains suitable for the
package's `start-stop-status` script. It returns 0 only when ready, 3 when
stopped, and 1 for backoff, recovery startup, a failed child, exhausted budget,
or unavailable supervisor. Its fixed diagnostics include
`DAEMON_RESTART_BACKOFF`, `DAEMON_RESTART_STARTING`, `DAEMON_RESTART_LIMIT`,
`DAEMON_CHILD_FAILED` and `DAEMON_CHILD_EXIT`. Package Center's exact text and
refresh behaviour are not established without device testing.

The new `control status-json <target> <var>` action returns a bounded JSON
snapshot and the same exit-code convention. Fields include `state`, `reason`,
`restarts` (total reservations in this supervisor lifetime), `retry_at` (UTC;
zero time when no attempt is pending), and the existing supervisor/child/birth/
config-digest inventory. States are `running`, `restarting`, `starting`,
`failed`, `exited`, `stopped` and `unavailable`. A live snapshot authenticates
the supervisor with the existing peer check and config digest. Saved terminal
state is diagnostic inventory, never readiness authority; a stale nonterminal
record becomes `unavailable` when the supervisor cannot be contacted.

An App backend can execute this fixed action as its package account and expose
the bounded state/reason fields through its existing authenticated status API.
Do not grant the browser direct access to process inventory or daemon logs.
No App UI integration is generated by this SDK change. Lifecycle diagnostics
are appended to the existing private `daemon/daemon.log`, alongside child
output; recovery does not introduce another log or rotation mechanism.

## DSM service integration decision

DSM documents non-root [systemd user units](https://help.synology.com/developer-guide/resource_acquisition/systemd_user_unit.html)
from DSM 7.0, installed from `conf/systemd/pkguser-*` and controlled with
`synosystemctl` in package scripts. Native `Restart=on-failure`, `RestartSec=`
and start rate limiting are available in systemd, and explicit systemd stops
do not restart a service. However,
[progressive restart delays](https://www.freedesktop.org/software/systemd/man/256/systemd.service.html#RestartSteps=)
(`RestartSteps=` / `RestartMaxDelaySec=`) require systemd 254. Their availability
on the supported DSM range is unverified.

The current CLI native-daemon path invokes the SDK's detached `control start`
from lifecycle scripts; it does not give systemd a foreground main process.
Adding `Restart=` to that launcher would monitor the short-lived controller,
not the owned daemon. Consequently this implementation keeps child recovery
inside that existing supervisor. It does not modify DSM-generated pkgctl units
or stack another systemd restart loop around it. Existing foreground user-unit
services should keep using native systemd restart where their requirements fit.
A future migration of native daemons to such units must first preserve package
stop/release ordering, original startup deadlines, readiness, diagnostics and
the finite retry budget; simply adding `Restart=` is insufficient.

## CLI and templates

Existing native-daemon configurations need no manifest, CLI or template changes:
rebuilding with this SDK enables the default policy in `daemonctl`. Existing
stop-before-upgrade/uninstall hooks retain their ordering. The CLI remains a thin
generator and does not implement its own retry loop. Custom policies in the
runtime JSON above require a generator that supports those fields; the default
policy does not depend on custom policy support in the CLI manifest.
