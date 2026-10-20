# Syno Package Land API 描述 v1

公開、與工具無關的互通規範。任何 SPK 可手寫或由自己的工具產生，不要求 Package Land 開發／SDK，也不以套件名稱判斷註冊。Package Land build 對四種標準 native runtime 自動封裝開發者明確選定的 commands。沒有有效描述就不列為註冊 App。

## 私有產物與固定端點

描述檔名稱固定 `synopkgland-api.json`，放在 SPK `package.tgz` 根目錄，安裝位置 `/var/packages/<INFO.package>/target/synopkgland-api.json`。不得放在 ui 或任何匿名靜態路徑。SDK 在以下固定端點提供描述：

```http
POST /webman/3rdparty/<package>/cgi/api.cgi
Content-Type: application/json
Cookie: id=<DSM session>
X-SYNO-TOKEN: <DSM synotoken>
Origin: https://nas.example:5001

{"method":"synopkgland.describe","params":{}}
```

驗證 DSM session、SynoToken 與 App entry 後，HTTP 200 JSON `{ "success":true, "data":<description> }`。未登入回 `{ "success":false, "error":{"code":"session_required"} }`；缺 token 回 `credential_not_ready`；管理員／App privilege 拒絕沿用後端 `admin_required`／`app_privilege_denied`。參數必須空物件。失敗不能回傳描述片段。Cache-Control: no-store，request／response 上限 1 MiB，禁止 redirects。

選擇完整描述＋逐 command／binding 標示 permission，讓 build hash 對各帳號一致。一般使用者仍須通過 App entry 才能讀；完整清單不承諾他能操作全部命令或每個資源。實際每一步照常檢查管理員、App Privileges、owner、ACL、grant。描述不是授權證明。

## JSON 格式與驗證

機器格式為相鄰 `synopkgland-api-v1.schema.json`。範例見 `synopkgland-api-v1.example.json`。外層必須為：

```json
{"format":"synopkgland.api","version":1,"payload":{},"sha256":"64 lowercase hex digits"}
```

payload 含 package{id,version,displayName}、aliases、provenance{developedBy,generator}、entry、transport、bindings、commands。package.id/version 逐字對應最終 INFO.package/version（包含 build），CLI 再與 DSM Package list 的 id/version 比對。build／inspect 驗證最終產物與紀錄，不接受只讀名稱前綴或 CGI 存在來推斷。

sha256 是移除頂層 sha256 後，對其餘 `{format,version,payload}` 使用 RFC 8785 JCS canonical UTF-8 JSON 的 SHA-256。它是內容識別／cache pin，**不是作者簽章**。不要 hash 整份 INFO：INFO checksum 指向含描述的 payload，會產生循環。developedBy 的 synopkgland／third-party／unknown 是自述，不能以產生工具當成開發者認證。

所有 JSON 必須是 UTF-8、無重複 key、有效 Unicode／有限數字，巢狀上限 32。描述最多 1024 個 commands／bindings，schema 不能引用外部 URL／檔案。paramsSchema／resultSchema 使用 JSON Schema 2020-12；$ref/$dynamicRef 只能引用本 schema 的 # 片段。驗 schema、hash、id/version、flow 與 endpoint 全部通過才能使用。

transport.path 固定 `/webman/3rdparty/<package.id>/cgi/api.cgi`，auth 是 dsm-session 或 dsm-app-session。v1 採固定路徑以避免驗證前相信描述中的任意 CGI；不接受 host、port、完整 URL、query、fragment、percent escapes、反斜線或點段。所有傳送都綁在使用者已註冊的同一 NAS origin。

## 任務指令，而非 API 方法選單

commands 是開發者**明確宣告**的工作。每個 command 包含：

- cli：字串陣列，例如 ["tasks","list"]；CLI 會在別名／package id 後使用此路徑。
- summary、effect（read/write/destructive）、permission、paramsSchema／resultSchema、examples（參數物件陣列）。參數可含 description／enum／default／examples；缺 schema 只接受 --input 並標「未描述參數」。
- flow：五種固定通用 adapter 之一，不允許 script、shell、任意 URL、表達式或任意步驟圖。

aliases 是不分大小寫的短名稱；package id 也不分大小寫。碰撞時不能猜，應改用唯一 package id；保留 host/list/help 等 CLI 關鍵字。錯字回最接近候選和完整 allowed。

bindings 是 commands 所需的內部方法 allowlist，只含 effect 與後端實際 permission；不作 CLI／AI 指令。未被任何 command 使用的 UI proxy、中間步驟、登入方法不自動暴露。每次實際呼叫都須在 bindings 中且通過後端原權限。command effect 不得比任何 binding 更寬鬆；job poll/result 必須 read。SDK standalone 的 identity.session/logout 是額外明確 auth bindings，不變成業務 commands。

## 固定組合流程

1. invoke：單次 JSON method；request `{method,params}`，成功 `{success:true,data}`／失敗 `{success:false,error:{code,message?}}`，HTTP 200 application/json。
2. job：start 一次取得 jobId 指定欄位，poll 依 idParam 查詢，state 為 done／failed 清單之一；完成後選填 result。僅允許欄位名稱路徑，不允許程式碼。--wait（預設）／--no-wait；timeout 1–3600 秒。逾時回報 job 仍可能運行，不重播 start。
3. paginate：固定 offsetParam／limitParam／items 欄位、pageSize／maxPages；只讀、最多 1000 頁／100000 項，達上限回不完整錯誤，不假稱完成。
4. ndjson：同一固定 CGI JSON POST，回 application/x-ndjson；逐行 JSON，CLI 有界收集成 items，最多 8 MiB／10000 行並設 timeout。此版收集，不提供任意串流轉換腳本。
5. upload：本機 regular file 透過 SDK 既有 raw-body protocol；POST 固定 CGI，query 僅由已驗 method、parent、name 組成，application/octet-stream、正確 Content-Length、同一 cookie/token/Origin。SDK 轉 FileStation multipart。最多 16 GiB，傳送不緩衝整檔、不自動重試。**目前 SDK 沒有通用 start/chunk/finish 協定，v1 不捏造此能力。**

上傳範例：

```json
{"summary":"Upload a local file","cli":["files","upload"],"effect":"write","permission":"authenticated","flow":{"type":"upload","method":"nas.files.upload","fileParam":"file","parentParam":"parent","nameParam":"name"},"paramsSchema":{"type":"object","required":["file","parent","name"],"properties":{"file":{"type":"string","description":"Local regular file"},"parent":{"type":"string"},"name":{"type":"string"}},"additionalProperties":false},"examples":[{"file":"./example.txt","parent":"/home","name":"example.txt"}]}
```

長工作範例（示範協定，不宣稱三個實際 App 已有這些 method 名稱）：

```json
{"summary":"Run a scan and wait for its result","cli":["scan","start"],"effect":"write","permission":"administrators","flow":{"type":"job","start":"scan.start","poll":"scan.status","result":"scan.result","jobId":"job_id","idParam":"job_id","state":"state","done":["completed"],"failed":["failed","cancelled"],"intervalMs":1000,"timeoutMs":60000},"paramsSchema":{"type":"object","required":["target"],"properties":{"target":{"type":"string"}},"additionalProperties":false},"examples":[{"target":"/volume1/example"}]}
```

App 特有流程應做高階 backend method；CLI 不寫 Gopeed／Reclaim／Connect 專用分支。cleanup 的 plan 身分與確認、BT 檔案選擇、provider 授權等不能被 generic adapter 或 --yes 取代。

## 探索、信任與快取

先 `ctl host add <name> --url https://...` 登入並存 OS 安全儲存。login/register 之外的操作須有已認證 host。沒有 current context：一台可省略 --host，多台必須 --host a,b 或 --all-hosts，host 並行上限 4。

每台 list 只呼叫一次 DSM Package list，得到當次帳號的候選與版本。首次發送描述前，用**一次批次核准**列出候選 package/version，核准只允許它們的固定描述 endpoint。非互動要有預先核准，或使用者明確執行 --approve-installed；agent 不自行核准。--app <package> 可縮小批次，不用逐包問。只有描述這個必要步驟可在 pin 前送 session；描述失敗不允許任何業務方法。

核准後，最多四個同時讀描述；單一錯誤／timeout 不阻塞其他 App，放 diagnostics。有效描述才列入 apps/commands，失敗套件不混入可用工具。首次有效 hash 存 pin；新版本需要批次更新核准，已 pin 的同版描述 hash 改變要明確更新核准。Cookie/token 不會因 redirect 或描述改 path 送往其他 App／主機。

cache key 包含 canonical host＋account＋package＋INFO version＋hash，以 package/version 索引 hash。同版直接用 cache，不重讀端點；--refresh 強制重讀。inventory 不見就不列出，跨帳號不共用。此模型假設同版本內容不可變；NAS 管理員手改同版本需 --refresh，hash 不提供對惡意 NAS 管理員的隔離。

## AI 操作與輸出

主要入口是 `ctl <alias|package> <cli path> --typed-flag value`；複雜參數 --input file 或 --input -。必填、型別、enum／schema 錯誤在 CLI 拒絕，未驗證不送業務 request。--help 與 ctl list --json 包含 schema／effect／permission／直接可執行的例子。write/destructive 沒 --yes 回確認錯誤與 plan；--dry-run 只回將送出的業務請求，不執行（仍可能需要已核准的描述探索）。

stdout 非 TTY 預設 JSON；--json 強制。單台成功 `{ok:true,data}`，失敗 `{ok:false,error:{code,message,hint,allowed?}}`，hint 給下一步指令。多台 `{ok,results:[{host,ok,data|error}]}`；任一失敗 exit 8，逐台顯示 App／command／version，不會因此重試已完成的 host。exit 2 認證、3 輸入／名稱、4 權限／信任、5 App/contract、6 網路、7 確認、8 部分失敗、1 內部錯誤。

host 管理：add、list、show、login、logout、remove、revoke。show 顯示核准／hash，不回 token；logout 留註冊／選用 device token；remove 非互動 --yes，刪 session/SynoToken/device token/approvals/pins/cache/註冊。rename 本版不提供。認證照 Naxos，唯 session=DSM 回 402 才省略 session 重試一次，403 提示六碼 OTP；其他錯誤不繞過。密碼／OTP 不保存、不中 argv/log，OS store 不可用不回退明文。

standalone 描述直接驗 DSM＋entry，不依賴 App cookie；業務命令以 allowlisted identity.session {bootstrap:true} 取得 host-only Secure HttpOnly App cookie，僅在記憶體、同包路徑使用並在結束 identity.logout。真實 HTTPS 要求不變，App cookie 不取代敏感方法的 DSM revalidation。

## 敏感參數與 writeOnly

App 開發者必須在參數 JSON Schema 以標準 `writeOnly: true` 標記 passkey、token、私人 URL、torrent/base64／上傳內容及其他敏感值；巢狀物件、陣列 items 與本機 `$ref` 同樣適用。不新增描述格式欄位，v1 envelope／hash 規則不變。

```json
{"type":"object","properties":{"url":{"type":"string","writeOnly":true},"uploads":{"type":"array","items":{"type":"object","properties":{"content":{"type":"string","writeOnly":true}}}}}}
```

CLI 以固定 `"<redacted>"` 顯示這些欄位，涵蓋 dry-run、缺少 `--yes` 的確認計畫、JSON／多主機輸出、schema 驗證錯誤與已知輸入值的回傳／錯誤文字；實際送給 App 的請求仍用原值。組合 schema 的任一可能分支有 writeOnly 時，輸出保守遮罩。若結果有其他敏感欄位，也在 resultSchema 標 writeOnly。CLI 不記錄或快取業務請求參數；對 schema default／const／enum／examples 和 command examples 也提供遮罩。請使用假值寫範例，不要把真實憑證嵌入描述文件。

描述快取若因遮罩改變內容，保留原始 hash pin 但標示為非可執行快取；下次先重新讀取並驗證原始 authenticated 描述，不能以遮罩後內容冒充通過原始 hash 驗證的描述。既有含敏感範例的快取讀取時會轉為此形式。遮罩是輸出規則，不代表 App 後端可以省略權限檢查。

## 命名建議

建議 CLI path 採 `<資源> <動詞>`。常用動詞可選 `list`／`get`／`export`（多為讀取）、`create`／`update`／`import`／`run`（多為寫入）、`delete`（多為破壞性）。同一 App 內同義動詞擇一，例如不要針對相同任務混用 show／get／status。**僅為建議，App 可用領域動詞，例如 pause、resume。**

每個 command 仍須由開發者明確宣告必填 `effect`；CLI／build 不從動詞推估或覆寫。既有「command effect 不得比 binding 寬鬆」等檢查維持，不新增動詞白名單或拒絕規則。
