# Self-package restart: unsupported contract and acceptance requirements

Studio Office needs a confirmed IT-assistant operation that restarts its own
package, retains an operation reference across loss of the requesting AI
connection, and reports what actually happened when the panel reconnects.

The SDK currently has no such operation. `daemonctl restart` controls an owned
child daemon, not the DSM package. A generic approved `webapi.call` does not
solve self restart: the DSM `SYNO.Core.Package.Control` contract provides
`start` and `stop`, and a package cannot reliably dispatch the second request
after its own stop has terminated its workers. Do not invent a `restart`
method, run arbitrary shell, create a DSM scheduler task, or report success
from an intent file or an unrelated later daemon start.

A future fixed SDK operation must pin the package from its installed policy,
require a fresh administrator and the current protected approval, and hand work
to a DSM-owned or independently supervised lifecycle executor that survives the
package being stopped. No caller-selected package, executable, arguments, account,
credential cache or generic command transport may cross that boundary.

Admission must durably pin a single operation reference before dispatch. A lost
reply must be reconciled through that reference, never resent. Status must
distinguish accepted, stopping, starting, confirmed running, refused, and unknown;
a new daemon's existence alone is insufficient proof of a successful restart.
The App preview must disclose that all its Apps, AI turns, transfers and workers
are interrupted, with a bounded sample of active jobs. Completion and failure
must be readable after the original browser/AI connection is gone.

Required device acceptance, before enabling this capability:

- Confirm a fresh administrator can request it and ordinary users cannot
- Refuse a changed/revoked policy before any lifecycle action
- Prove the executor survives the target's stop and starts the same package once
- Lose the admission reply and race repeated callers without restarting twice
- Disconnect the AI/browser and read the original result after reconnecting
- Prove stop/start failures and uncertain outcomes never claim success
- Exercise package stop and reboot during every admission/execution phase

This document records the unresolved upstream dependency, not an implemented
restart API. No self-package restart is enabled by this PR. A DSM acceptance
run requires separate explicit device authorization.
