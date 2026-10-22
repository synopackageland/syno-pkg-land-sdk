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
