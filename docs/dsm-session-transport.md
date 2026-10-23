# DSM session transport and file migration

Browser DSM sessions must not be replayed by a backend HTTP client. DSM can
bind a session to its source address and revoke it on an address mismatch.
Do not rely on particular DSM versions exempting localhost or HTTPS, change
the NAS security setting, forge forwarding headers, or substitute an admin
or package account.

## Browser file operations

`createNasProvider(ctx)` and `FilesPage` keep their existing UI contracts.
They check App admission with `nas.session.status`, then call DSM directly
on the browser's current origin. The status route reports
`{mode: "browser-session"}` and performs no loopback request.
`createBackgroundPickerProvider` and executor scope selection also read share
metadata directly from DSM. Remove any `bg.browse` → `nas.files.shares`
adapter; neither is used to relay file metadata anymore.

The existing `ctx.files.token` callback remains reactive. Its old `endpoint`
value is ignored. Outside a desktop, the transport can recover the token
from same-origin DSM `login.cgi`. It never reads a cookie in JavaScript,
persists credentials, chooses another origin or falls back to a backend.
The page must be served on an origin that provides DSM WebAPI and a DSM
browser session. An App's OIDC or standalone identity session is not one.

For custom interfaces, use the typed headless client:

```ts
import {createDsmFileStation, createFileTransfers} from '@synopackageland/sdk'

const files = createDsmFileStation({token: ctx.files?.token})
const signal = new AbortController().signal
const shares = await files.shares(0, signal)
const page = await files.list('/team', {offset: 0, limit: 100}, signal)
const entry = await files.info('/team/report.txt', signal)
await files.createFolder('/team', 'Reports', signal)
await files.checkPermission('/team/Reports', signal)
// Browser permission observations are display information, not backend grants.

const transfers = createFileTransfers({token: ctx.files?.token})
await transfers.upload('/team', selectedFile, signal)
await transfers.download('/team/report.txt')
transfers.dispose()
```

The client fixes the DSM endpoint and typed API/method/version combinations.
JSON replies are bounded to 1 MiB. Metadata requests use same-origin
credentials, reject redirects, honor cancellation and do not retry.
Uploads use browser multipart File bodies, retain the 16 GiB limit, disable
overwrite/parent creation, and report `commit-uncertain` after an ambiguous
dispatch. Native downloads POST the token in a sandboxed iframe with a
same-origin form-action policy; no token enters the URL and files are not
buffered into a JavaScript blob. `download()` now returns a Promise for
preparation/submission errors, **not completion or a checksum**; await it.
`FileBrowser` does this and keeps its existing current-file info check.

## Existing privileged destinations

Destination selection must remain server-authorized. `delegate.Service` no
longer calls the deprecated `SessionFiles.CheckPermission` before
`SelectDestination`. The existing executor's `delegate.create` already
verifies the current session, account, App Privileges, approved scope and
write permission as the verified subject before creating the grant.

Apps that already have an approved `files.moveFromStaging` executor can
recheck an existing destination without relaying a browser cookie:

```go
err := saves.CheckDestination(ctx, caller, request, "save", folder)
```

This resolves the current caller-bound grant and calls the executor's
`files.checkPermission`. For an until-revoked destination this operation
omits `job_id`, requires the current owner's browser session, accepts only
the exact granted destination, and rechecks current UID, App Privileges,
scope generation, directory identity, write permission, disabled state and
revocation. It is a read-only observation under the existing
`files.moveFromStaging` policy; it cannot create or widen a grant, pick a
runner, read files or write data. Legacy time-limited grants still require
their original `job_id` and explicit operation policy. A successful check
does not replace the final write's own permission and revocation fences.

Rebuild the package's executor along with the SDK. This executor change is
subject to the normal protected-payload digest and approval flow; do not
reuse an old executor or bypass approval. This is not a release instruction.
Browser-only Apps must not acquire a root helper to migrate file operations.

## App rebuild checklist

| Consumer | Required changes after updating the SDK |
| --- | --- |
| Download Hub (Gopeed) | Keep `FilesPage`, `createNasProvider` and save picker; remove the share-picker `bg.browse` remapping. Remove `SessionFiles: filestation.Local(...)` from save assembly. Replace direct `Files.Verify/Stat/CheckPermission` destination checks in previews, task submission and rechecks with `saves.CheckDestination(ctx, caller, request, "save", folder)`; preserve current grant/job binding. Return owner-checked published-job navigation metadata from `gopeed.reveal`, then verify the path with browser `createDsmFileStation().info` before navigation. Rebuild the existing executor and retain normal approval. |
| OpenList DSM console | Keep `capabilities.files: session`, `filestation.Local(...).Routes()` for the admission/status route, `FilesPage` and `createNasProvider`. Picker, info, create folder, upload and download now go directly from the browser to DSM. Convert any custom nas.files.* calls to the typed browser client; keep account bridge/OIDC admission unchanged. **Do not enable root, delegation or an executor.** |
| Reclaim | Its current rootOps-only capability does not enable the retired File Station relay. Keep native DSM verification and existing root policy. |
| Other identity gateways | Keep native authorize-CGI authentication and DSM cookie/header filtering. The gateway is not a generic DSM session proxy. |

Updating Go dependencies alone does not migrate custom App handlers or custom
frontend transport overrides. The rebuild must include the new frontend SDK
and the call-site changes above. Validate every currently used journey before
shipping an App; this SDK change does not certify an installed NAS package.

## Retired backend session relay

`filestation.NewClient`, its typed methods (including the separate streaming
upload), and default `filestation.Local` file operations return `unsupported`
before HTTP, source-reader, spool or file I/O. Signatures remain for source
compatibility; there is no unsafe enable flag. Custom `DSMClient` adapters
remain possible but must supply independent, caller-bound authorization.
Authentication alone never authorizes a NAS filesystem path.

The not-currently-enabled server-only `bg.browse` / `ReadFile` scaffold and
default bridge `files.list`, `files.info`, `files.read` services likewise
refuse rather than replay sessions. Python, Node and Rust App wrappers all
use this same Go runtime; they never receive DSM cookies or tokens.
Move interactive browsing to the frontend and send selected **untrusted**
paths back only to a separately authorized operation. An unused import
feature can stay unsupported; do not migrate an active feature by removing
its permission check or substituting package-account access.

## 驗證與遷移摘要

互動檔案功能改由瀏覽器同源呼叫 DSM；選取路徑或前端權限檢查結果不是後端授權。
Gopeed 既有升權 executor 可用 `CheckDestination` 重查原擁有者、原目的地與即時
權限；OpenList 只用前端同源路徑，不新增 root／executor。只有未啟用的 server-only
scaffold 及預設 bridge 檔案服務先回 `unsupported`。所有語言 wrapper 共用相同邊界。

App 重建須同時更新前後端 SDK、修改上表列出的自訂呼叫，並驗收選目的地、share
picker、NAS 檔案頁、info/reveal、上下傳與權限拒絕。不要修改 DSM 安全設定作為修法。
Fixture／單元測試可驗證不重送與授權契約；`skip_ip_checking=false` 的實際 session
保留、DSM 版本、portal 與代理拓撲須另行實機驗收。
