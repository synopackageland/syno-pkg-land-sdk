# W1a frozen v1 extension

Authority remains the existing `{v:1,op,auth,params}` JSON-line protocol. No new
protocol version, credential store, or identity platform. This fixture is a DTO
vector, not deployment, DSM ACL, reboot, or completed-file evidence.

Canonical definitions: `src/delegate/long-running.ts` and `src/protocol/v1.ts`; golden frame:
`v1.json`. Legacy `{scope,operations}`/TTL/job declarations stay unchanged.
Long-running declarations explicitly require all four fields, only
`files.moveFromStaging`, `lifetime: until-revoked`, and nonempty unique destination
keys matching `^[a-z][a-z0-9_-]{0,63}$` (at most 64 keys, matching the backend
build configuration). No implicit permanent conversion.

Home scope paths use File Station's `/home` virtual root (for example
`/home/exports`). `/homes/<username>/...` is not an accepted executor home-scope
vector. The server resolves the verified subject's physical home; callers never
construct that path from a username.

Picker facade `selectDestination({key,path})` maps to
`delegate.select_destination`. The backend checks the live subject and write
permission and calls `delegate.create` with `{scope,ops,path,destination,lifetime}`
(destination is the manifest key); no job/TTL on the permanent branch. Response
uses the existing executor `delegation_id`; facade returns the `DestinationGrant`
DTO. `delegate.list/revoke` stay owner-bound; audit is owner-filtered unless the
verified live subject is an admin. App payloads never choose owner or UID.

`moveFromStaging` maps to `delegate.move_from_staging`, then executor
`files.moveFromStaging`. Admission returns only `{job_id,state}`. Status/cancel
routes `delegate.operations.get/cancel` map to `files.operationStatus/cancelOperation`
with the original `{delegation_id,job_id}`. Timeout/unknown completion queries the
same operation; it never creates a new ID. Committed is never downgraded by source
cleanup failure; final path comes only from the executor receipt.

Disable/enable/audit use `delegate.disable/enable/audit` on both layers. Disable
blocks legacy and new admissions; mailbox start cannot enable. Admin is its own
non-root UID. Root operations remain separate. Preserve existing refusal codes;
use `disabled`, `operation-conflict`, `operation-not-found`, `queue-full`,
`invalid-destination`, `invalid-size`, `interrupted`, and `uncertain` for these
new cases. Receipts/audit must never contain live session credentials.

Policy extensions are top-level `destinations` and `lifetime` beside existing
`scope`; policy digest pins all four manifest fields. W1b emits build strings
`main.delegateScope`, `main.delegateOperationsJSON`, `main.delegateDestinationsJSON`,
`main.delegateLifetime` for CGI and the fixed worker. W2 owns the Go string
variables/parser/config wiring. Worker has one official user-unit owner even
when a package daemon coexists. W1b activates these policy/route IDs only for the explicit opt-in.

Required negative vectors for W2/W3/W4: unlisted key/scope; forged owner/UID;
other owner's list/revoke/status/cancel; session subject mismatch; failed write
check; same job with changed input; disabled legacy/new admission; revoke/publish
race; copying recovery without replay; symlink/hardlink/FIFO/parent replacement;
size+1 actual bytes; committed plus failed source cleanup. W4 owns the separate
two-App completion fixtures; this directory is W1-owned.

W4 adds CGI fixture vectors for protected `delegate.status.disabled` and the
strict-empty-params `delegate.operations.list`. The latter lists own-user move
mirrors only (admin too), with no receipt; live status uses the original reference
and `files.operationStatus`. It adds no executor list operation.
