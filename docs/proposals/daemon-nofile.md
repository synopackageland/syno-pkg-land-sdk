# Non-root daemon file descriptor limit

Status: proposal; no runtime or packaging behavior changes in this PR.

A daemon launched from Package Center inherits the package control service's
resource limits. Two development Controllers report soft/hard NOFILE 4095/4096
for the non-root worker. With the application's current reservation formula,
that admits 991 sessions. A 3000-session target requires at least 10120 file
descriptors. Raising only the soft limit inside daemonctl cannot raise the
inherited hard limit. The current SDK correctly makes no such claim.

## Supported DSM mechanism

DSM documents system units shipped at `conf/systemd/pkg-<name>.service`:
Package Center acquires them into `/usr/local/lib/systemd/system` and releases
them with the package. This is distinct from `systemd-user-unit`, whose user
manager cannot raise a hard limit above its own inherited limit.

Use an opt-in CLI-generated system unit with an explicit package `User` and
`Group`, `LimitNOFILE=32768`, and `NoNewPrivileges=yes`. PID 1 sets the limits
before dropping privileges; the application, daemonctl supervisor and worker
remain non-root. Do not edit `pkgctl-<package>.service`, system-wide defaults,
PAM, users, or lifecycle scripts to run root commands. Do not request capabilities
or add an executor operation merely to call setrlimit.

Sources:
- https://help.synology.com/developer-guide/resource_acquisition/systemd_user_unit.html
- https://www.freedesktop.org/software/systemd/man/256/systemd.exec.html

## Proposed ownership and implementation

References below use SDK `bc9ebdefaf7850c30d3793469e6fa1ca1e17f0e0` and CLI
`98bed79c457daf5f4f683edb1d321da34b3e9f63`. Re-check lines on implementation.

- CLI `src/standalone/daemon.ts:34`: add optional integer `daemon.nofile`,
  validate a bounded range (1024 through 1048576); omit it to preserve existing
  behavior. Reject unsupported launch kinds rather than silently ignoring it.
- CLI `src/standalone/lifecycle.ts:375`: route start/stop/restart through
  `synosystemctl` for this opt-in unit; keep SDK status and uninstall semantics.
  Verify the package account may control only its acquired unit. Stop the outgoing
  unit before replacing payloads. Existing upgrade and failure fencing must hold.
- CLI `src/standalone/spk.ts:472`: include the generated `conf/systemd/pkg-...`
  unit in the SPK control archive. Current native manifests refuse arbitrary DSM
  conf (`manifest.ts:375`); do not bypass that validation from an application.
- CLI `src/standalone/background.ts:43`: this user-unit implementation is a
  reference for generation only; adding LimitNOFILE here cannot solve daemon
  hard-limit inheritance. Do not change other background jobs in this work.
- SDK `daemonctl/main.go:445` and `:512`: the supervisor and child naturally
  inherit limits from the system unit. No root work or timeout changes are
  needed in these paths. Any status addition should expose observed soft/hard
  values, not claim the manifest request was applied.

Candidate unit shape (generation must escape/validate all package identifiers):

```ini
[Unit]
Description=Example package daemon

[Service]
Type=oneshot
RemainAfterExit=yes
User=Example
Group=Example
Slice=Example.slice
LimitNOFILE=32768
NoNewPrivileges=yes
UMask=0077
ExecStart=/var/packages/Example/target/daemon/control start /var/packages/Example/target /var/packages/Example/var
ExecStop=/var/packages/Example/target/daemon/control stop /var/packages/Example/target /var/packages/Example/var
KillMode=control-group
```

The generated TimeoutStartSec/TimeoutStopSec must derive from the existing
validated SDK operation budgets. Do not introduce another timeout policy.
Confirm DSM's control/acquire order before treating this candidate as supported.

## Acceptance required before enabling real ingress

1. Generator tests cover opt-in/omitted/invalid values, both launch paths,
   package-account identity, exact budgets and acquired unit names.
2. SPK inspection confirms the fixed unit, package User/Group, and no added
   privilege, root hook, custom command or arbitrary unit/drop-in support.
3. Upgrade a leased development package in place; never uninstall or delete
   Controller data. Read the actual worker `/proc/<pid>/limits` and UIDs after
   upgrade and restart. Both limits must cover 10120; target 32768.
4. Verify ordinary package start/stop/status, old-version upgrade cleanup,
   failed startup and package stop leave no orphan supervisor or duplicate writer.
   Validate on both development devices. Keep ingress disabled until this passes.
5. Run the same 3000-session probe under the resulting non-root worker and retain
   actual FD/RSS/session measurements. Unit text alone is not acceptance.

No persistent system setting was changed for this investigation. A transient
systemd-run proof could not run because that utility is absent on the devices.
This proposal does not assert that the installed worker's limit has changed.
