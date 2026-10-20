# Proposal: bounded Compose account actions (not implemented)

This change only delivers nonroot configuration/prepare. DSM account deletion
revokes new admission and keeps upstream accounts/data; there is no container
account deletion hook in this release.

A future service action should register a fixed image-owned argv at build time,
bound to package, Compose project, service, generation and package UID. Only typed
data substitutions such as a validated numeric UID may vary. Native authorization
must prove the exact installed image/service/UID before execution. Never expose
a Docker socket, root runtime, free-form shell, arbitrary container selector or
unrestricted exec endpoint to the App/gateway.

Account deletion should first revoke gateway admission, then durably enqueue a
generation-bound action. Retried deletion needs explicit upstream absent-result
mapping (not arbitrary exit-code success), bounded time/output, cancellation,
auditable sanitized status and manual retry. Keep protected/default accounts
undeletable. UID reuse must not attach retained data to a new person; ownership
requires an account-generation/tombstone policy, not email merging.

The open question is an official DSM authorization/execution surface with those
boundaries. Until such a controller is proven, service exec remains unsupported;
the package retains data and reports the limitation instead of adding privileged
container access. A future native upstream admin API could be an alternative
with the same subject and lifecycle guarantees.
