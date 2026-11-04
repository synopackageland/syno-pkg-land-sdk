# Native account lookup

`dsm.LookupAccount(ctx, principal)` reads one account through the SDK's embedded
Linux NSS/DSM bridge, as the package account. It needs no administrator grant.
It does not enumerate accounts, authenticate a password, or replace application
privilege checks.

The returned `dsm.AccountInfo` contains `Exists`, `Username`, `UID`, `GID`, `Home`,
`Admin`, `Expired`, `Groups` (numeric GIDs), and `Source`. IDs are `uint32`, including
on ARMv7. Local accounts are proved against the root-owned local passwd database.
Domain accounts are proved against winbind, including a fresh controller ping.
The bridge also checks forward/reverse identity agreement and rechecks identity
after reading expiry, administrator status and groups.

`Expired` preserves the bridge's conservative admission policy: `"normal"` means
no expiry date and no current expiry; `"now"` includes disabled accounts, elapsed
expiry **and future expiry dates**. It does not distinguish these cases.
`AccountEnabled` uses the same lookup and returns true only for `"normal"`.
Disabled accounts still return a complete snapshot from `LookupAccount`.
`Admin` is DSM's administrator decision, including directory administrators;
callers should not substitute membership in a single local GID.

```go
ctx, cancel := context.WithCancel(ctx)
defer cancel()
account, err := dsm.LookupAccount(ctx, principal)
switch {
case errors.Is(err, dsm.ErrAccountTimeout):
    // Keep the previous observation; retry later.
case errors.Is(err, dsm.ErrAccountNotFound):
    // NSS reports absence. Reconcile without deleting historical assignments.
case err != nil:
    // Unavailable or canceled. Do not infer enabled/disabled state.
default:
    // Use account.Source, account.Admin, account.Groups and account.Expired.
}
```

Without a context deadline, the default is the shared five-minute operation budget.
An explicit caller deadline overrides it in either direction; cancellation is
honored. The shared allowance covers an observed 12.4 second lookup on a memory-starved controller (median about 0.7 seconds).
It is a latency budget, not a freshness guarantee. Request paths can use a shorter
deadline; background reconciliation should bound concurrency and use the default
or an explicit longer deadline. There is no SDK cache or automatic retry.

| Outcome | Result | Error (`errors.Is`) |
| --- | --- | --- |
| Existing normal or restricted account | Complete snapshot, `Exists=true` | nil |
| Initial NSS lookup reports absence | Empty snapshot | `ErrAccountNotFound` |
| Deadline exceeded | Empty snapshot | `ErrAccountTimeout` and `context.DeadlineExceeded` |
| Caller cancels | Empty snapshot | `context.Canceled` |
| NSS/provider failure, unsupported provider/platform, invalid name, malformed output or identity change | Empty snapshot | `ErrAccountUnavailable` |

NSS absence is a provider observation, not proof of remote deletion: offline
providers and negative caches can hide accounts. Do not turn a lookup error into
an enabled/disabled decision or automatically delete a stored person. A later
failed identity recheck remains unavailable, rather than not-found.

## LDAP boundary

The source vocabulary is `local`, `domain`, `ldap`. This implementation returns
successful snapshots only for verified local and winbind domain accounts.
`AccountSourceLDAP` names the provider for consumer integrations; it does not
claim native LDAP support.

DSM can resolve LDAP identities through libc NSS and its SSSD provider (older
implementations use nslcd). Resolving an identity alone does not prove online
account status: caches can survive provider outages, and the DSM LDAP shadow
path can fall back to a non-expiring result after a lookup failure. This bridge
therefore retains the winbind ownership/online check for every nonlocal account.
LDAP failures return unavailable; a successful-looking name is never classified
by its spelling.

Native LDAP support needs a provider-specific proof of identity, online state,
and reliable disabled/expiry status under the package UID, including outages,
missing shadow attributes and configured LDAP profiles. Skipping the winbind
check is insufficient. Until that contract is verified, use the consumer's
administrator-delegated DSM LDAP lookup and keep unavailable observations
separate from disabled status. Do not read binding credentials or connect to LDAP
directly from the application.

## Release integration

This additive Go API requires matching SDK and executor releases.
No version metadata, tags or published artifacts are changed by this work.
The account bridge payloads in both `dsm/native` and `executor/native` change;
the next batched release must build and publish a fresh executor inventory rather
than reuse an executor without this capability, then regenerate the SDK executor manifest/digests.

After that release, Studio should update its pinned SDK dependency, use
`LookupAccount` for background checks/reconciliation and DSM's returned `Admin`
and `Groups`, retain its previous observation on lookup errors, and keep the
delegated LDAP path. Select deadline and cache policy at the call site.
