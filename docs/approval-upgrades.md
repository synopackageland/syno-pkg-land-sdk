# Upgrade approval explanations

Before releasing an executor App, run the CLI with a previous SPK from the same
package and architecture:

```sh
synopkgland build --arch x86_64 --baseline previous.spk --json
synopkgland inspect current.spk --baseline previous.spk
```

Include the approval summary in release notes. It compares root program bytes,
policy permissions/parameters, task templates and INFO-anchored executor-only
handover. No baseline means retention is unknown. Eligibility is conditional on
the installed runtime and exact DSM task readback; the report grants nothing.
Optionally place the same summary in the manifest `metadata.changelog` for Package Center.
DSM is not assumed to open the App automatically after upgrading it.

The shared `ExecutorConsent` displays an upgrade explanation before the existing
password form. `ExecutorApproval` restores its original content after the same
existing ready check. All nine SDK locales have update, permission and unknown
messages. A root-program digest change alone does not mean declared permissions
expanded. Added operations or programs are labelled expanded. Other policy
changes require review; arbitrary scope/schema changes cannot safely be classified
as narrowing. Program behavior can change even with the same declared policy.

The root monitor reads the previous policy from its protected runtime and hashes
the candidate policy, root programs and INFO-anchored executor. It compares its
canonical task templates with the candidate setup. It atomically publishes a
bounded, root-owned, group-readable `/run/synopkgland/<package>/approval.json`
while checking the package pins, including when the operation socket remains
paused. The file carries candidate policy/executor digests. The SDK backend reads
only a regular, non-symlink file in a root-owned directory, rejects writable files,
and returns it to administrators through `executor.status`. The CLI window accepts
the explanation only when it matches the current package's pins.

If the socket is absent and the protected comparison shows a changed policy,
the client immediately reports the executor as unavailable for approval. A
recent lifecycle `start` does not delay this result: `delegate.status` reports
`needs-admin-approval`, and `executor.status` returns the paused state with the
comparison visible to administrators. An explicit stop still takes precedence.
An unchanged policy retains the normal start grace, including INFO-anchored
executor upgrades, and a listening executor always supplies its own status.
The password field stays editable during status reloads and locks only while
approval is being submitted; loading remains visible on the settings section.

This file is diagnostic data, never a grant, task inventory or handover input.
No approval, digest, scheduler, PasswordConfirm or root execution rule uses it.
Missing, oversized or unverifiable diagnostics fall back to an explicit unknown
explanation on an outdated approval. Old runtimes predating this feature cannot
provide protected comparison data retroactively; their first migration reports
unknown. A candidate that fails verification cannot be described as safe.

`permissions: unchanged` compares the entire policy except root digest values;
`expanded` identifies added operations or root programs; `review-required` covers
other changes. Root digests and JSON-pointer policy differences are included.
Diagnostics contain no passwords or session credentials and live under `/run`,
so reboot removes them. They do not cause DSM to launch a window.

Package-account lifecycle scripts are not an approval pin. A change to a
generated `start-stop-status` alone does not change root authority when the
policy, protected programs, executor anchor and exact scheduler definitions
still match. Inspect must nevertheless recognize the entire lifecycle script;
the CLI accepts only explicitly supported, byte-exact released variants. An
arbitrary lifecycle edit still makes a baseline invalid. Packages without a
daemon retain their original lifecycle bytes when daemon stop handling changes.

An approval refusal is distinct from the unavailable executor it may leave
behind. The SDK retains the scheduler error and rollback outcome even if a
subsequent `delegate.status` reports `EXECUTOR_UNAVAILABLE`. Administrators can
select Retry to read the current state; Retry neither resends a password nor
creates or runs a task. A failed rollback is never permission to run a task.

For `dsm_105`, sign out of DSM, sign in through the DSM desktop, and check the
status before approving again. Approval uses that window's native WebAPI
transport and password confirmation, independently of the App's RPC fetch.
DSM releases can additionally require a session-bound request hash for Root
scheduler calls from non-private addresses. A password confirmation token does
not replace that hash. Keep the desktop session, its token and its native
credential together; do not replace this path with a root command or a separate
password login. A persistent denial requires administrator diagnosis, not an
automatic reboot or a fallback to another scheduler API.

New CLI windows include the refused API, method, version and numeric DSM code
in `CapabilityStatus.diagnostic`, without request parameters or credentials.
The original error remains available if rollback also fails. Rollback success
requires an exact readback of the restored tasks, and withdrawal success requires
readback of their absence.
