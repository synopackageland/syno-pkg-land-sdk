# Non-root daemon NOFILE

The SDK implementation in this PR decodes optional `nofile` in the fixed daemon
configuration, validates 1024–1048576, and enforces the requested soft limit
before starting its supervisor's worker. Omission preserves inherited limits.
The controller and every child continue to require the package's non-root UID.
An insufficient inherited hard limit refuses startup with the bounded diagnosis
`DAEMON_NOFILE_INSUFFICIENT`; it never raises the hard limit or requests privilege.
Status, stop and uninstall remain usable from ordinary low-limit package hooks.

The paired CLI change exposes `daemon.nofile: 32768` in dsm-package.yaml and
acquires `conf/systemd/pkg-<package>-daemon.service`. PID 1 sets LimitNOFILE before
dropping to the fixed package User/Group. The unit calls the existing daemonctl
start/stop paths; their validated deadlines remain the sole work budgets. No
root lifecycle hook, executor action, arbitrary unit, user selector or command
is added. Applications must update to the published supporting CLI/SDK before
opting in. An old SDK or mismatched local override must fail packaging.

DSM mechanism: [official acquired units](https://help.synology.com/developer-guide/resource_acquisition/systemd_user_unit.html).
A user unit alone cannot exceed its user manager's hard limit: [systemd resource limits](https://www.freedesktop.org/software/systemd/man/256/systemd.exec.html).
Never edit generated pkgctl services, system-wide defaults or PAM to apply an
application's limit.

Implementation:

- `daemonctl/nofile.go`: bounded configuration and non-root inheritance policy
- `daemonctl/nofile_linux.go`: read inherited limits, raise only soft within hard;
  explicitly set it so Go exec does not restore its original low soft limit
- `daemonctl/main.go`: strict decoder, validation and pre-worker enforcement
- `daemonctl/nofile_other.go`: explicit unsupported opt-in outside Linux

Tests exercise invalid/omitted/boundary values and the actual controller/child
as a non-root user. A 4096 hard ceiling refuses 32768; a 65536 hard ceiling with
an initial 1024 soft limit admits the worker with at least 32768 soft, unchanged
65536 hard and nonzero UID. Low-limit callers can still query status and stop.
The complete daemonctl suite is run as non-root; generator tests in the paired
CLI cover acquired unit identity, packaging, upgrade paths and tamper refusal.

Device acceptance follows coordinator merge and publication: opt in in an App,
upgrade leased development packages in place, read actual worker UIDs and
soft/hard limits after upgrade/restart, and check status/stop/failed-start and
old-version upgrade behavior. Do not infer acceptance from unit text, Linux
fixtures or manifest intent; do not enable ingress until its gates pass.
