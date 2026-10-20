# 多語言 App SDK 設計（Go runtime + Python／Node／Rust 薄層）

狀態：設計與原型，P1 Go runtime 正式版（2026-10-13）：`spec/bridge/v1` 已凍結，`bridge.NewRuntime` 與 `bridge/cmd/runtime` 已完成，只有 exec 模式（§1.1）。隨 SDK 0.10.0 發布。
協定：`spec/bridge/v1/`。Go runtime：`bridge/`。App SDK：`rust/app/`（Rust 薄層 `syno-pkg-land-app`，P6 正式版，見 `rust/app/README.md`）；`python/`（`synopkgland_app`）、`node/`（`@synopackageland/app`）自 0.10.0 起為正式版。Rust 完整 SDK（`rust/sdk`）已在 P6 移除，最後的 tag 是 v0.9.9。

## 1. 結論

- **採用建議的架構，驗證可行。** Go runtime 仍是 CGI，原封不動重用 `api.Server`：來源檢查、DSM 登入、entry gate、`permissions.json`、請求期限、JSON 限制與錯誤封套都在 App 拿到請求之前完成。App 只多了一種 route 實作（`bridge.Routes`），`api` 套件一行都沒改。
- **跨語言一致性已有證據。** `testdata/golden/api.jsonl` 的 35 個案例中，32 個經過通道送到 Go、Python、Node、Rust 四種 App，exec 與 socket 兩種模式，回覆與原生 Go 逐位元組相同。另 3 個是串流下載（原型尚未做 transfer，見 §6）。Python 與 Node 有 1 個已知的數字字面值差異（`2.50` → `2.5`，見 §5.3），已在 runner 中明列；Rust 保留字面值，沒有差異。
- **模式：v1 全部用 exec。** 依使用者訂的門檻「每個 App 常駐多出的記憶體超過 5 MB 就全部改用 exec」，§3.3 實測常駐 App 的私有記憶體（USS）：Python 4.5–6.3 MB、Node 17–52 MB、Rust 0.2–0.8 MB。Python 與 Node 超過門檻，所以全部用 exec。代價是 exec 每個請求多付一次啟動：Python 在 x86 NAS 約 53 ms、ARM NAS 約 155 ms；Node 約 141 ms 與 513 ms；Rust 只有 0.45 ms 與 3.2 ms。socket 模式保留在協定與原型裡，v1 不出貨（§5.2）。
- **Rust：完整 SDK 已移除（P6），改成同一條通道上的薄層。** Rust 薄層 SDK 約 335 行；Rust App 走 exec，整個 CGI 請求比原本的 Rust 完整 SDK 多約 4.5 ms（x86）與 9.8 ms（ARM），主要是 Go runtime 的 CGI 啟動（§3.2）。Reclaim 的遷移計畫見 §13。

### 1.1 P1 正式版的現況

- **只有 exec。** socket 模式的程式碼（runtime、三個 SDK、bench 的記憶體量測）已刪除，因為 v1 不出貨、沒有使用者，只增加維護與審查面。協定只保留 `SYNOPKGLAND_BRIDGE=socket:<path>` 這個保留值。下文 §3.3、§5.2 的 socket 量測與設計是 早期原型的紀錄。
- **每個請求多花的時間（exec，§3.1）**：

  | App | x86 NAS | ARM NAS |
  |---|---|---|
  | Rust | 約 0.5 ms | 約 3 ms |
  | Python | 約 53 ms | 約 155 ms |
  | Node | 約 141 ms | **約 0.5 s** |

  Node 在 ARM NAS 上每個請求多約 0.5 秒。對延遲敏感的 App（列表捲動、輸入即時查詢、輪詢進度）建議用 Python 或 Rust。
- **manifest**：`bridge.json`（`spec/bridge/v1/manifest.schema.json`）放在 `/var/packages/<pkg>/target/app/`，與 `permissions.json`、App 本身同一個目錄，不在 DSM 會提供的 `ui/` 下。CLI 以 `-X` 把套件 ID 與兩個檔案的 SHA-256 綁進 runtime，不符就拒絕啟動。
- **runtime 組的東西**：DSM authenticator、manifest 的 entry gate（`access`）、`permissions.json`、請求期限（`timeout_seconds`）、請求大小（`max_request_bytes`），以及 `capabilities` 開的 SDK 路由（`files` → `nas.*`、`identity` → `identity.*`、`portal` → `portal.status`），然後是 App 的方法。App 方法不可與 SDK 方法同名（manifest 驗證時就拒絕）。
- **App 行程**：以套件帳號執行（runtime 拒絕以 root 執行），環境變數只有 spec 列出的那些加上 manifest 的 `env`（不能覆寫 runtime 的），自己一個 process group，請求期限一到就 SIGKILL 整個 group（測試 `TestDeadlineEndsTheAppAndWhatItStarted`、`TestManifestTimeoutEndsTheCall`）。
- **`app_unavailable`**（`spec/bridge/v1/errors.json`，9 語系）：App 無法啟動、在 `result` 前結束，或在啟動逾時（預設 10 秒）內沒有送 `hello`。請求期限先到時仍是 `request_timeout`，與 Go App 一致。放在 bridge 自己的 catalog，不放 `spec/protocol/v1/errors.json`：後者會改動 `rust/sdk` 的產生碼與 TS 的公開代碼表。
- **App 可回的代碼**：自己的小寫代碼，或 `spec/protocol/v1/errors.json` 中 kind 為 `refusal` 的大寫代碼（`internal/specgen -bridge` 產生 `bridge/refusals_gen.go`）。
- **gate**：跨語言 golden runner 與 hello 測試只跑 exec，hello 經過完整的 `NewRuntime`（Python、Node、Rust）。

## 2. 架構

```
瀏覽器 ─HTTP→ DSM nginx ─CGI→ Go runtime（套件帳號）
                                   │ api.Server：CheckOrigin、PrepareTransferRequest、
                                   │ Authenticator（dsm.Authenticator + EntryGate）、
                                   │ permissions.json、deadline、jsonlimits
                                   │ SDK 自有路由：identity.*、nas.files.*、bg.*、
                                   │ delegate.*、portal.*、executor.*（Go 實作，不經通道）
                                   ▼
                          bridge.Routes（App 的方法，來自 manifest）
                                   │ spec/bridge/v1 通道
                     ┌─────────────┴─────────────┐
               (a) exec：每請求一個行程      (b) socket：常駐 App
               stdin/stdout                  /var/packages/<pkg>/var/app.sock 0600
                     └─────────────┬─────────────┘
                     App（Python／Node，套件帳號）
                     只收到已驗證的 call；需要權限的事以 svc 請 runtime 代辦
```

重點：

1. **SDK 自有路由不經通道。** `api/sdk-permissions.json` 的路由（檔案、背景工作、delegate、portal、identity、executor）由 runtime 直接以 Go 實作註冊，前端 `@synopackageland/sdk` 照舊呼叫。換語言不影響這些能力，也不必在 Python／Node 重寫。
2. **App 的方法清單來自建置時的 manifest，不來自 App。** runtime 依 manifest 註冊 route，握手時要求 App 回報的清單與 manifest 完全相同，否則視為協定錯誤。App 無法在執行期新增 `permissions.json` 沒看過的路由（未列出的方法本來就預設 administrators）。
3. **一個通道只有一個 call。** exec 與 socket 共用同一份 session 程式（`bridge.Session`），差別只在 `Dialer`。

### 2.1 與 Rust 版的比較

| 選項 | 共用邏輯 | 每新增一種語言的成本 | 風險 |
|---|---|---|---|
| A. 各語言完整 SDK（現況 Go＋Rust） | 每種語言各一份 | 1–2.7 萬行＋golden 對齊 | 安全邏輯分岔；已見 Go 1.7 萬／Rust 2.7 萬行重複 |
| B. Go runtime＋通道＋薄 SDK（本設計） | 只在 Go | 約 150–350 行＋型別產生（Rust 薄層 335 行） | 每請求多一跳；exec 模式每請求多一次行程啟動 |
| C. Go 編成 C ABI（cgo `c-shared`）給各語言 FFI | 只在 Go | FFI 綁定＋各架構 .so | DSM 舊 glibc、armv7 交叉編譯、Node 需 N-API addon；CGI 仍要 Go 以外的入口，安全邊界在同一行程內，App bug 可破壞 runtime 記憶體 |
| D. WASM（App 編成 WASI，Go 內嵌 wazero） | 只在 Go | 語言要能編 WASI | Python／Node 的 WASI 支援不成熟、啟動慢、生態套件不可用 |

B 的安全邊界是行程邊界：App 拿不到 cookie／SynoToken，也碰不到 runtime 的記憶體。C、D 沒有這個保證，或排除了目標語言的一般生態，因此不建議。

## 3. 量測

### 3.1 每個請求多花的時間

方法：`bridge/internal/bench`，在同一個 Go 行程內經 `api.Server` 處理一個 `hello.greet` 請求，比較原生 Go handler 與經通道的 hello App。CGI 行程本身的啟動與 `authenticate.cgi` 在所有模式都相同，**不計入**；表中數字即「改用 Python／Node 多付的時間」。每項 5 次，取中位數與最大值（ms）。

| 機器 | 環境 | 負載（前→後） |
|---|---|---|
| 本機 | Apple M 系列 macOS，Python 3.14.7、Node 24.14.0 | — |
| Linux container | x86_64 12 核，container `node:24-bookworm-slim`，Python 3.11.2、Node 24.21.0，uid `node` | 0.38 |
| x86 NAS | DSM 7.4-90069，geminilake x86_64 2 核，kernel 4.4.302+，系統 Python 3.8.15、套件 Node.js_v22（22.22.3），uid `nobody` | 2.77 → 2.77（有其他工作） |
| ARM NAS | DS416，alpine armv7l 2 核，DSM 7.1.1-42962，kernel 3.10.108，系統 Python 3.8.12、套件 Node.js_v18（18.18.2），uid `nobody` | 1.12 → 1.25 |

第二輪（Rust 與記憶體）在兩台 NAS 上重跑，負載：x86 NAS 0.89 → 0.98（計時）、0.88 → 1.06（記憶體）；ARM NAS 1.29 → 1.64、1.20 → 2.10。Rust binary 以 `cargo zigbuild --release` 編成 `x86_64-unknown-linux-musl` 與 `armv7-unknown-linux-gnueabihf.2.17`。Rust 只在兩台 NAS 量。

| 項目（中位數／最大值 ms） | 本機 | Linux container | x86 NAS | ARM NAS |
|---|---|---|---|---|
| Go 原生 handler | 0.01 / 0.09 | 0.03 / 0.14 | 0.03 / 0.12 | 0.10 / 0.45 |
| Python 直譯器空啟動 `-c pass` | 25.8 / 34.0 | 15.4 / 15.9 | 36.5 / 37.1 | 96.2 / 145.0 |
| **Python exec（每請求）** | 52.6 / 57.2 | 40.0 / 40.4 | **54.6 / 155.7** | **161.5 / 172.1** |
| Python 常駐啟動到可連線（一次） | 46.7 | 40.5 | 53.6 | 157.9 |
| Python socket 第一個請求 | 0.56 | 1.32 | 0.89 | 3.25 |
| **Python socket（每請求）** | 0.27 / 0.40 | 0.86 / 1.10 | **0.66 / 0.76** | **2.95 / 3.47** |
| Node 直譯器空啟動 `-e 0` | 38.3 / 47.8 | 40.8 / 45.3 | 110.4 / 1187.6 | 480.3 / 625.0 |
| **Node exec（每請求）** | 41.0 / 58.5 | 56.6 / 63.6 | **148.7 / 151.5** | **525.4 / 535.8** |
| Node 常駐啟動到可連線（一次） | 48.3 | 51.6 | 151.5 | 513.4 |
| Node socket 第一個請求 | 2.10 | 6.59 | 7.32 | 31.93 |
| **Node socket（每請求）** | 0.30 / 1.26 | 1.09 / 4.44 | **1.18 / 4.39** | **5.59 / 15.73** |
| **Rust exec（每請求）** | — | — | **0.45 / 0.74** | **3.19 / 3.59** |
| Rust 常駐啟動到可連線（一次） | — | — | 2.66 | 12.08 |
| Rust socket 第一個請求 | — | — | 0.35 | 1.20 |
| **Rust socket（每請求）** | — | — | **0.28 / 0.76** | **0.60 / 0.93** |

第二輪在 NAS 上重量的 Python／Node 數字與第一輪接近（x86 NAS：Python exec 53.2、Node exec 140.9；ARM NAS：154.9、512.8 ms），原始資料都在 thread 的 `library/measurements.txt`。

觀察：

- x86 NAS 上 Node 空啟動的最大值 1187 ms 是第一次執行（頁快取冷），之後約 100 ms。
- exec 的成本幾乎等於直譯器啟動＋載入 SDK；通道本身的往返在 socket 列可見，x86 約 1 ms 內。
- Rust 沒有直譯器，exec 只多一次 fork／exec 與握手：x86 NAS 0.45 ms、ARM NAS 3.2 ms，比 Python socket 還快或相當。

### 3.2 Rust：薄層 vs 現行完整 SDK

基準是 早期原型的 `rust/sdk/examples/hello-baseline.rs`：同一個 hello 直接寫在現行 Rust SDK 的 `api::Server` 上。P1 已移除這個範例，P6 再移除整個 `rust/sdk`；bench 只保留 Go runtime 的整個 CGI 列（`-cgi`）。比較兩種量法：

- **行程內**：與 §3.1 相同，不含 CGI 啟動。
- **整個 CGI 請求**：bench 像 DSM 的 web server 一樣以 CGI 環境變數與 stdin 啟動 CGI，量到讀完回覆為止。兩邊都用假的 authenticator，所以不含 nginx 與 `authenticate.cgi`；那兩段在新舊架構都一樣。

| 項目（中位數／最大值 ms） | x86 NAS | ARM NAS |
|---|---|---|
| Rust 完整 SDK，行程內 | 0.01 / 0.03 | 0.02 / 0.12 |
| Rust 薄層 exec，行程內多花 | 0.45 / 0.74 | 3.19 / 3.59 |
| Rust 薄層 socket，行程內多花 | 0.28 / 0.76 | 0.60 / 0.93 |
| **整個 CGI：Rust 完整 SDK（現況）** | **0.31 / 0.38** | **2.48 / 10.65** |
| 整個 CGI：Go runtime，原生 Go handler | 3.51 / 5.38 | 8.51 / 8.86 |
| **整個 CGI：Go runtime＋Rust App exec** | **4.80 / 7.11** | **12.24 / 12.77** |
| 整個 CGI：Go runtime＋Rust App socket | 4.37 / 6.85 | 9.71 / 10.58 |

結論：

- 改走通道後，Rust App 每個請求多約 **4.5 ms（x86）與 9.8 ms（ARM）**。
- 其中大部分是 Go runtime 的 CGI 啟動比 Rust 慢（3.2 ms 與 6.0 ms）。這段成本現在每個 Go App 都已經在付。通道 exec 本身只多 1.3 ms 與 3.7 ms。
- 相對 DSM WebAPI 往返的一般延遲，這個差距不影響使用者體驗。所以 Rust 也用 exec，不需要常駐行程。

### 3.3 常駐模式多用的記憶體

方法：`bench -mem`。每個 App 以 socket 模式啟動 5 次。每次在開始 listen 後等 200 ms 讀一次（閒置），再處理 100 個 `hello.greet` 後讀一次。數字取自加總 `/proc/<pid>/smaps` 的 `Rss`、`Pss` 與 `Private_Clean`＋`Private_Dirty`（USS）；ARM NAS 的 3.10 kernel 沒有 `smaps_rollup`。單位 MB，中位數／最大值。

| App | 機器 | 閒置 RSS | 閒置 PSS | 閒置 USS | 100 請求後 RSS | 100 請求後 PSS | **100 請求後 USS** |
|---|---|---|---|---|---|---|---|
| Python | x86 NAS | 9.4 / 9.4 | 6.1 / 6.1 | 5.3 / 5.3 | 10.5 / 10.6 | 7.0 / 7.1 | **6.1 / 6.3** |
| Python | ARM NAS | 5.3 / 5.3 | 4.6 / 4.6 | 4.5 / 4.5 | 6.1 / 6.2 | 5.3 / 5.4 | **5.3 / 5.3** |
| Node | x86 NAS（v22） | 48.5 / 48.7 | 42.4 / 42.5 | 40.5 / 40.6 | 60.3 / 60.6 | 54.2 / 54.4 | **52.3 / 52.5** |
| Node | ARM NAS（v18） | 19.1 / 19.1 | 17.5 / 17.5 | 17.2 / 17.2 | 21.1 / 21.1 | 19.5 / 19.5 | **19.2 / 19.2** |
| Rust | x86 NAS | 0.6 / 0.6 | 0.6 / 0.6 | 0.6 / 0.6 | 0.8 / 0.8 | 0.8 / 0.8 | **0.8 / 0.8** |
| Rust | ARM NAS | 0.7 / 0.7 | 0.2 / 0.2 | 0.2 / 0.2 | 1.0 / 1.0 | 0.5 / 0.5 | **0.5 / 0.5** |

不改 SDK、只改啟動參數的壓低方法：

| 做法 | x86 NAS 100 請求後 USS | ARM NAS 100 請求後 USS | 取捨 |
|---|---|---|---|
| Python `-S`（不載入 site） | 6.1（−0.0） | 5.2（−0.1） | 幾乎沒效果；SDK 本身只 import 標準函式庫的 json、os、socket、sys、threading、traceback |
| Node `--max-old-space-size=8` | 51.1（−1.2） | 19.2（0） | 幾乎沒效果；heap 本來就小，App 一變大反而會 OOM |
| Node `--jitless` | 46.1（−6.2） | 18.6（−0.6） | 閒置不變；關掉 JIT 後 CPU 密集的 App 會慢數倍，且停用 WebAssembly |
| Node 三者合用（再加 `--max-semi-space-size=1`） | 43.5（−8.8） | 18.6（−0.6） | 仍遠高於 5 MB；GC 變頻繁 |

結論（「多耗多少」）：

- 以 **USS** 計算「多耗」。exec 模式在請求之間不佔記憶體，所以常駐 App 的私有頁就是多出來的量；RSS 還包含與其他行程共用的函式庫，會高估。PSS 是把共用頁按比例分攤後的系統成本，比 USS 多約 1–2 MB。
- **Python：4.5–6.3 MB**，剛好落在 5 MB 門檻附近，x86 超過。
- **Node：17–52 MB**，遠超門檻。這是 V8 本身的基本成本，與 SDK 無關，啟動參數最多只省 9 MB。
- **Rust：0.2–0.8 MB**，遠低於門檻。
- runtime 沒有常駐的部分：兩種模式下它都是每個請求一個 CGI 行程。
- 依門檻，**全部改用 exec**。Rust 本來就不需要常駐，因為 exec 每請求只多 0.45–3.2 ms。

## 4. 通道協定

完整規則在 `spec/bridge/v1/README.md`，frame 的 JSON Schema 在 `spec/bridge/v1/frames.schema.json`。測試 `bridge/schema_test.go` 檢查 runtime 送出的每種 frame 符合 schema，且 runtime 的解碼器剛好認得 App 可送的欄位。

- **框架**：一行一個 UTF-8 JSON，上限 16 MiB + 64 KiB（= `api.MaxRequestBytesCeiling` 加封套）。
- **握手與版本協商**：runtime 送 `{"t":"hello","versions":[1]}`，App 從中選一個回覆，並附 `sdk` 與 `methods`。未來 v2 時 runtime 送 `[1,2]`，舊 App 回 1 仍可用；App 回不在清單內的版本即協定錯誤。每個通道都握手：exec 本來就是新行程；socket 的握手只多一次本機往返（已含在上表）。
- **call**：`{"t":"call","id":"1","method":…,"params":…,"ctx":{user, admin, locale, request_id, deadline_ms}}`。
- **svc／svc_result**：App 在 result 前可發任意多個 svc；runtime 依序處理並以相同 `id` 回覆。
- **cancel**：請求期限到或瀏覽器斷線（`context.Canceled`）時 runtime 送 `cancel`，給 App 1 秒（`CancelGrace`）收尾，之後關通道；exec 模式殺整個行程群組。
- **result**：`ok` 與 `data` 或 `error`。runtime 以 Go encoder 重新編碼後回瀏覽器，所以 HTML 安全跳脫、`Content-Length` 等行為與原生 Go 相同（golden 證明）。

## 5. 請求生命週期

1. DSM 啟動 CGI（套件帳號）。`api.WithRequestDeadline` 或 `api.Server` 設期限（預設 60 秒）。
2. `api.Server` 做完所有檢查；拒絕的請求不會啟動或連線 App（測試 `TestRefusedRequestsNeverReachTheApp`）。
3. `bridge.Routes` 的 handler 組 `Context`：`user`、`admin` 取自 `api.Caller`；`locale` 是 `Accept-Language` 的第一個 BCP 47 標籤（不合法就給空字串）；`request_id` 是 128 位元亂數；`deadline_ms` 是送出時剩餘的毫秒數。
4. 連線 App、握手、送 call。
5. App 處理；需要時發 svc。SDK 提供：
   - Python：`ctx.user`、`ctx.admin`、`ctx.locale`、`ctx.request_id`、`ctx.deadline_ms`、`ctx.deadline`（請求結束時的 `time.monotonic()` 值）、`ctx.cancelled`（`threading.Event`）、`ctx.runtime(op, params)`。
   - Node：欄位是 camelCase：`ctx.user`、`ctx.admin`、`ctx.locale`、`ctx.requestId`、`ctx.deadlineMs`、`ctx.deadline`（`Date.now()` 毫秒）、`ctx.cancelled`、`ctx.signal`（`AbortSignal`，`reason` 為 `deadline`／`canceled`）、`await ctx.runtime(op, params)`。
6. App 送 result；runtime 關通道並回瀏覽器。

### 5.1 逾時與取消

- 期限由 Go 的 `context` 決定，App 只會收到「剩餘時間」與 cancel 訊號；App 無法延長期限。
- Python 無法中斷正在執行的函式，handler 應定期檢查 `ctx.cancelled`；不檢查也無妨，1 秒後 runtime 關通道（exec 會殺行程）。
- Node 的 `AbortSignal` 可直接傳給 `fetch`、`fs` 等 API。
- 瀏覽器端看到的仍是 `request_timeout` 或 `request_canceled`，與原生 Go 一致。

### 5.2 socket 模式的常駐行程

**v1 不出貨 socket 模式**（§3.3 記憶體門檻）。以下設計留給之後；若某個 App 需要低延遲，再以 manifest 選配開放。

- 由套件的 start-stop-status／DSM service 以套件帳號啟動 App，`SYNOPKGLAND_BRIDGE=socket:/var/packages/<pkg>/var/app.sock`。CGI 不負責啟動常駐行程（CGI 是短命行程）。
- SDK 以 umask 0177 建 socket，再 chmod 0600；runtime 連線前檢查檔案型別、權限、擁有者，連線後以 `SO_PEERCRED`（Linux）／`LOCAL_PEERCRED`（macOS）確認對端 uid 與自己相同（測試 `TestSocketRefusesAnOpenSocket`）。
- App 掛掉時 runtime 回 `internal`（正式版建議新增 `app_unavailable`，見 §12）。**不建議自動退回 exec**：兩種模式行為不同（常駐狀態、記憶體），退回會掩蓋故障。
- 升級：DSM 升級套件會 stop → 替換 → start，期間的請求得到 `app_unavailable`。

### 5.3 數字

Python 與 Node App 拿到的是值不是字面值：`2.50` 變 `2.5`；JavaScript 超過 2^53 的整數失真。golden runner 把這個差異寫成明確的例外。Rust 薄層以 `serde_json::value::RawValue` 傳遞 params，保留原始字面值，與 Go 逐位元組相同。建議：

- schema 規範 64 位元 ID、檔案大小、時間戳以字串表示（產生型別時也是 `string`）。
- 不提供「保留字面值」模式；Python 沒有可行的方法，Node 要 Node 21+ 的 `JSON.parse` source access，DSM 的 Node.js_v18 不支援。

## 6. 大檔案傳輸（P2，已實作）

需求：上傳、下載、`?method=` transfer 不能整個讀進記憶體，也不能把位元組塞進 JSON frame（base64 膨脹 33%，還要受 frame 上限限制）。

選項：

| 選項 | 上傳 | 下載 | Python | Node | 評估 |
|---|---|---|---|---|---|
| A. 暫存檔（spool） | runtime 串流寫入 0600 暫存檔，call 帶路徑 | App 寫暫存檔並回傳檔名，runtime 以已知長度串流回去 | 可 | 可 | 兩模式一致；長度已知，符合 DSM CGI 需要 `Content-Length` 的限制；多一次磁碟寫入 |
| B. exec 額外 fd（fd 3 讀、fd 4 寫） | 直接串流 | 直接串流 | 可 | 可（`fs.createReadStream(null,{fd:3})`） | 零拷貝；只適用 exec；下載長度未知，需先由 App 宣告 |
| C. socket SCM_RIGHTS 傳 fd | 直接串流 | 直接串流 | 可（3.8 用 `recvmsg`） | **不可**（Node 無原生 API） | Node 不支援，排除 |
| D. 分塊 frame | 不建議 | 不建議 | | | 記憶體與 CPU 成本高 |

**v1 採 A（spool）**，規格見 `spec/bridge/v1/README.md` 的 Transfers：

- manifest 以 `transfers: {"<method>": "upload"|"download"}` 宣告，`max_upload_bytes` 可把上限調低（預設與上限都是 `filestation.MaxUploadBytes` 16 GiB）。transfer 方法只走 `?method=`，JSON 呼叫回 `use HTTP transfer endpoint`。
- 暫存目錄 `<data>/bridge-spool/`（volume 上，不在 tmpfs）：0700 目錄、0600 檔案、`<request_id>.<用途>`，以 `O_EXCL|O_NOFOLLOW` 建立；目錄不是套件帳號的 0700 目錄就拒絕。call 結束必刪；CGI 被殺留下的檔案在下次 transfer 時清掉超過 2 小時的。
- 傳輸資訊放在 runtime 組的 `params`，不放 `ctx`（`context` frame 是 `additionalProperties:false`，且現有 SDK 只複製已知欄位；放 params 讓三個 SDK 不必改）：
  - 上傳：`POST`、`application/octet-stream`、必須有 `Content-Length` 且不超過上限。App 收到 `{"query":{…},"upload":{"path","size"}}`，照一般 JSON 回答。
  - 下載：`GET` 或 urlencoded 表單 `POST`。App 收到 `{"query":{…},"download":{"path"}}`，把內容寫到 `path`，回 `{"name","content_type"?}`；runtime 以 `O_NOFOLLOW|O_NONBLOCK` 開檔、必須是一般檔，設 `Content-Length`、`Content-Disposition: attachment`、`no-store`、`nosniff` 後串流。
  - `query` 去掉 `method`、`SynoToken`、`_sid`（不分大小寫）；重複的 key 是 `bad_request`。
- 期限與取消：讀寫都經 `contextReader`，請求期限一到就停。
- 存到使用者選的共用資料夾：App 把結果寫進自己的資料目錄（或 delegate 的 `executor-staging`），再以 svc `delegate.move_from_staging` 交給 executor，沿用 `MoveFromStaging`／`DiscardStaging` 的語意。
- B 列為 v2 的選配，用於影音串流等長度未知的下載。

## 7. App 可請 runtime 代辦的服務（P3，已實作）

原則：只開放「需要 runtime 持有的東西」的服務——DSM 工作階段、executor 連線、SDK 的工作日誌。App 自己做得到的（讀寫自己的資料目錄、HTTP 對外）不必經 runtime。所有服務都綁定在當下的 call：呼叫者是 runtime 驗證過的那位，需要時以該請求的 `api.DSMSession` 代填 `auth`；App 永遠看不到 cookie 與 token，也不能指定別的使用者。服務表與參數見 `spec/bridge/v1/README.md` 的 Services；每項由 manifest 的 capability 開啟。

| 服務（svc op） | 對應 Go | capability | 理由 |
|---|---|---|---|
| `files.list`、`files.info` | `nas.files.list`／`nas.files.info` 路由（`api.Route.Invoke`） | `files: "session"` | 需要使用者的 DSM 工作階段；權限由 DSM 判斷 |
| `files.read` | `filestation.Service.Download` → spool | `files: "session"` | 同上；結果是 spool 檔，call 結束刪除 |
| `delegate.status`、`delegate.move_from_staging`、`delegate.operation` | `delegate.*` 路由 | `delegate: {…}` | 與瀏覽器路由同一段程式；executor 的 scope、APP_DENIED 等檢查不變 |
| `delegate.discard_staging` | `delegate.Service.DiscardStaging` | `delegate: {…}` | 放棄未搬的 staging |
| `bg.submit`、`bg.get`、`bg.cancel` | `background.Store`（`Submit`、`GetFor`、`CancelFor`） | `jobs: true` | 工作日誌、配額、所有權由 Go 管；擁有者是呼叫者 |
| `root.run`、`root.start`、`root.jobs.list`、`root.jobs.get`、`root.jobs.stop` | `delegate.Client`（executor 協定） | `root: true` | Reclaim 遷移需要（coordinator 決定）。runtime 只轉送並代填 `auth`；executor 向 DSM 驗證，依 policy 與 App 權限決定 |
| `portal.status` | `portal.Read` | `portal: true` | 唯讀 |
| App 自訂背景工作 | `background.Runner` + 通道 | 否（v2） | worker 也要經通道呼叫 App；需另定 worker 生命週期 |
| `identity.*`（App 登入／session） | `identity.Service` | 否 | 已由 SDK 路由服務瀏覽器；App 只需要 `ctx.user` |
| 其他原始 executor op、`executor.control` | `delegate.Client` | **否** | 會繞過 SDK 的 scope／policy 組裝 |
| DSM cookie／SynoToken／`authenticate.cgi` | `api.DSMSession` | **否** | App 拿到 token 就能冒充使用者 |
| 以其他使用者身分呼叫 | — | **否** | `user` 只來自 runtime 驗證 |
| 設定任意 HTTP 標頭／Cookie | `api.HandleResponse` | **否** | 下載標頭由 runtime 依 App 回的 `name`、`content_type` 產生 |

`executor.status`、`executor.control` 不是 App 的服務，而是 `root: true` 時 runtime 自己提供給瀏覽器的路由（`delegate.ExecutorRoutes`，與 Go App 同一段程式）：CLI 的 root window 核准後會呼叫它們並比對 status 的 pins，App 拿不到 executor 自報的 digest，也不該寫 lifecycle mailbox。App 的 manifest 不能列這兩個 method。

共通限制：params ≤ 64 KiB，`root.run`／`root.start` ≤ 1 MiB（`request_too_large`）、成員嚴格（`bad_request`）、期限與取消同 call、`portal.status` 以外在 `public` 方法（無登入者）回 `session_required`、服務 panic 是 `internal`。

錯誤：svc 失敗時 `error.code` 是 runtime 的代碼。executor 背景的服務傳回 `spec/protocol/v1/errors.json` 的 code（如 `SCOPE_DENIED`、`ROOT_BUSY`）；SDK 的連字號代碼為符合 `svc_result` 的 pattern 改成底線（`limit-exceeded` → `limit_exceeded`）。SDK 以 `Error`／`AppError` 拋出，App 可依 code 判斷。

## 8. 錯誤模型

| 來源 | 代碼 | 到瀏覽器 |
|---|---|---|
| runtime 的拒絕（來源、session、權限、期限、大小） | `api` 小寫代碼：`session_required`、`admin_required`、`cross_origin`… | 原樣 |
| App 的業務錯誤 | App 自訂 `^[a-z][a-z0-9_]{0,63}$`，訊息 ≤ 1024 bytes | 原樣 |
| App 回傳保留代碼（上列 runtime 代碼） | — | `internal`（避免 App 讓前端誤以為要重新登入） |
| App 拋出非 `Error` 例外、協定錯誤、行程崩潰 | — | `internal`；細節只進 stderr（DSM 日誌） |
| svc 的 executor 拒絕 | `errors.json` 大寫代碼 | App 決定是否轉成自己的代碼 |

待決：是否允許 App 直接把 `errors.json` 的 refusal 代碼（大寫）回給瀏覽器，讓 `@synopackageland/sdk` 的 i18n 直接翻譯。原型不允許（只收小寫）。建議允許「`errors.json` 中 kind 為 refusal 的代碼」，由 runtime 以內嵌的 `errors.json` 白名單檢查。

## 9. 權限邊界與威脅模型

前提：runtime 與 App 都以套件帳號執行；兩層權限不變——無升權的 App 任何階段不用 root；root 清理只給 executor App，且只經 executor 協定與已核准的 policy。

| App 惡意或有 bug 時 | 能 | 不能 |
|---|---|---|
| 讀寫 | 套件帳號本來就能讀寫的檔案（自己的 var、target） | 拿到瀏覽器的 cookie／SynoToken（exec 不繼承 CGI 環境，測試 `TestExecRunsTheAppWithOnlyTheGivenEnvironment`；socket 模式 App 本來就在 CGI 之外） |
| 回應 | 對**已通過檢查**的請求回傳任意資料或錯誤 | 回應被拒絕的請求（拒絕發生在連線 App 之前）；冒用 runtime 的拒絕代碼；超過 jsonlimits |
| 路由 | 實作 manifest 列出的方法 | 新增方法或改權限（握手比對 manifest；`permissions.json` 由 runtime 讀） |
| 身分 | 讀 `ctx.user`／`ctx.admin` | 以別人身分呼叫服務；呼叫未開放的服務（`unknown_service`） |
| 升權 | 在 manifest 宣告 `root: true` 時，以**當下請求的使用者**請 executor 跑 `root.*`；能否執行由 executor 向 DSM 驗證 session 後，依 policy 與 App 權限決定 | 以 root 執行（runtime `background.RefuseRoot`；SDK 也拒絕）；呼叫其他原始 executor op；在請求之外或以別人的 session 呼叫 `root.*`（`auth` 由 runtime 填，App 的 params 無法帶） |
| 檔案 | 經 `files.*` 讀呼叫者在 File Station 能讀的檔；經 `bg.submit` 把資料目錄內的檔當輸入 | 讀呼叫者無權讀的檔（DSM 判斷）；經 `bg.submit` 讀資料目錄外的檔（`os.OpenRoot`，連結出不去）；讀別的 call 的 spool 檔以外的東西（spool 本來就是套件帳號的） |
| 資源 | 耗 CPU／記憶體到期限為止；exec 模式每請求一個行程 | 超過期限：runtime 送 cancel、1 秒後殺行程群組 |
| socket | — | 被其他帳號冒充：runtime 檢查 socket 檔與對端 uid |
| executor socket | — | 改變 runtime 連的 executor socket：正式 api.cgi／worker 固定用 `/run/synopkgland/<package>/executor.sock`，不讀請求、CGI 變數或環境（`TestNoRequestChoosesTheExecutorSocket`）；只有 Go 測試 API `bridge.Config.ExecutorSocket` 能改 |

殘餘風險：

- 同一套件帳號下的其他行程（例如套件自己的其他元件）可以連 socket 或改 App 檔案。這與現況 Go App 相同：套件帳號是信任邊界。
- 常駐 App 可在請求之間保留狀態，包括上一個使用者的資料。SDK 文件需提醒不要把 `ctx` 存成全域。
- App 可以把 `data.write` 之類的服務當成一般寫檔；正式版移除 `data.write`。
- 拒絕服務：常駐 App 卡住會讓所有請求逾時。建議 runtime 對 socket 連線加併發上限，並由 start-stop-status 監控重啟。

## 10. 打包（CLI）

`synology-app.yaml`／`dsm-package.yaml` 新增：

```yaml
backend:
  runtime: python        # go（預設，現況）| rust | python | node；rust 指薄層 App，不再是完整 SDK
  entry: app/main.py      # 相對 backend/；rust 為 CLI 交叉編譯出的 binary
  mode: exec              # v1 只有 exec（§3.3）；socket 留待之後選配
  methods: [hello.greet, hello.save]   # 或由 CLI 在建置時執行 `entry --describe` 取得
  python: { min: "3.8" }
  node: { package: Node.js_v20 }
```

CLI 的工作：

1. **runtime binary**：CLI 用與 App 相同的 `go.mod` 版本，從 `github.com/synopackageland/syno-pkg-land-sdk/bridge/cmd/runtime` 為 SPK 的每個 arch 交叉編譯（`CGO_ENABLED=0`，amd64／arm64／armv7，與 executor 一樣可重現），`-X main.packageID=…`、`-X main.manifestDigest=…`、`-X main.permissionsDigest=…`，並綁定 SDK policy digest（沿用 `expectedSDKPermissionsDigest`），裝成 `ui/cgi/api.cgi`。不同 arch 的 SPK 各帶對應的 runtime；App 程式碼本身 noarch。
2. **manifest**：寫成 `target/app/bridge.json`（格式見 `spec/bridge/v1/manifest.schema.json`），與 `permissions.json` 一起由 runtime 在啟動時讀取。CLI 以 `--describe` 執行 App（建置機上）核對方法清單與 `permissions.json`。
3. **SDK 檔案**：
   - Python：把整個 `synopkgland_app/` 目錄 vendoring 到 `app/vendor/synopkgland_app/`（不是單檔；不經 pip，DSM 沒有 pip 也沒網路假設），並把 App 的 `PYTHONPATH` 設成 `vendor` 目錄。
   - Node：SDK 沒有依賴；把 `package.json` 與 `dist/` 複製到 `app/node_modules/@synopackageland/app/`，或用 esbuild 把 App 與 SDK 打包成單檔。
4. **INFO 相依**：
   - Node：`install_dep_packages="Node.js_v20"`（套件中心套件名；量測的 x86 NAS 裝有 v20、v22，ARM NAS（DSM 7.1）裝有 v18）。runtime 以 `/var/packages/Node.js_v20/target/usr/local/bin/node` 執行，不依賴 `PATH`。建議 SDK 支援 Node 18 起，CLI 讓開發者選 `Node.js_v18|v20|v22`。
   - Python：DSM 7.1、7.4 都有系統 `/usr/bin/python3`（兩台實機皆 3.8.x）。Synology 未把它列為第三方可依賴的公開介面，未來可能改版；建議預設用系統 python3 並在 runtime 啟動時檢查版本 ≥ 3.8，另外提供 `python: { package: Python3.9 }` 走套件中心。
5. **start-stop-status**：socket 模式產生啟停 App 的腳本（以套件帳號），exec 模式不產生常駐行程。
6. **範本**：`synopkgland init -t native-python`／`native-node`，沿用 native-go 的前端、root-ops、delegate overlay（後端換成 App 程式碼＋runtime 設定），capability overlay 機制不變。

## 11. 型別產生

需求：從 schema（App 的方法 params／result 與 SDK 服務）產生 Python 與 TypeScript 型別。來源建議用 JSON Schema（已是 `spec/protocol/v1` 的格式），不引入 OpenAPI。參考 cloudflare/forge 的做法（單一 schema＋overlay 產生多語言），只借思路。

| 工具 | 語言 | 輸出 | 依賴／執行環境 | 評估 |
|---|---|---|---|---|
| json-schema-to-typescript | TS | `.d.ts` 介面 | Node，建置時 | 成熟、輸出乾淨；不產生執行期驗證 |
| quicktype | TS／Python／Go／Rust… | 型別＋（部分語言）序列化碼 | Node，建置時 | 一套工具多語言；對 `oneOf`、`const` 的支援較弱，輸出風格難控制 |
| datamodel-code-generator | Python | pydantic v2／dataclass／TypedDict | Python 3.9+，建置時 | 成熟；輸出 `TypedDict` 時 App 不需 pydantic（DSM 3.8 無 pip） |
| Zod 產生器（json-schema-to-zod） | TS | 執行期驗證 schema | Node | 有驗證但 App 要依賴 zod |
| 自寫產生器（類似 `internal/specgen`） | 任意 | 自訂 | Go | repo 已有先例；可精準控制輸出與 0 依賴，但要自己維護 |

建議：TS 用 json-schema-to-typescript、Python 用 datamodel-code-generator 輸出 `TypedDict`（App 不需執行期依賴，且相容 3.8 語法）。兩者只在建置機（CLI）執行。執行期驗證交給 runtime（Go 已有 JSON 解碼與限制），App 不重複。

## 12. 正式版前要補的缺口

- ~~`bridge/cmd/runtime`~~、~~`app_unavailable`~~、~~refusal 代碼白名單~~：P1 已完成（§1.1）。
- ~~transfer（§6 spool）~~、~~svc 服務表（§7）~~、~~Python 版本檢查~~：已完成。
- ~~bridge 套件的背景 worker~~：已完成，`api.cgi --worker` 以套件帳號執行 `delegate.Service.Run`（spec §Background worker）；由 CLI 產生的 systemd user unit 啟停。
- `root.*`：`root.start`、`root.jobs.*` 的名稱與參數以 executor spec 為準；Go 的 `delegate.Client` frame 上限 64 KiB，root.run 允許 1 MiB，要等它更新 client。
- 前端：`@synopackageland/sdk` 尚無 `app_unavailable` 的翻譯（npm 套件不帶 bridge spec），P5 時一併處理。
- （socket 模式出貨時才需要）socket 併發上限、常駐 App 監控。
- ~~跨語言 runner 的 svc 與 transfer 案例~~：已完成（`bridge/golden_bridge_test.go`）。

## 13. Rust：這一輪移除完整 SDK

決定（使用者，第二輪）：這一輪就移除 Rust 完整 SDK。Rust App 改成和 Python、Node 一樣的薄層，走同一條通道。早期階段只做原型、量測與計畫；P6 已刪除 `rust/sdk`，Reclaim 仍未動，釘在 v0.9.7。

### 13.1 Rust 薄層（P6 正式版）

- crate `syno-pkg-land-app`（`rust/app/`，約 335 行，依賴只有 serde、serde_json、libc）：`App::method`、`Context{user, admin, locale, request_id, deadline_ms, cancelled(), runtime(op, params)}`、`Error`、`Params`（`RawValue`，保留字面值）。P6 起只有 exec（`SYNOPKGLAND_BRIDGE` 必須是 `stdio`），`ctx.runtime::<T>` 解碼服務結果（`Box<RawValue>` 保留字面值），`ctx.deadline()`，`refusals` 由 `spec/protocol/v1/errors.json` 產生；拒絕以 root 執行，handler panic 回 `internal`。文件：`rust/app/README.md`。
- `rust/app/examples/hello.rs`（`hello.greet`、`hello.save`）與 `golden.rs`。跨語言 runner 與 hello 端到端測試都跑 Rust exec，回覆與原生 Go 逐位元組相同。
- 速度與記憶體見 §3.2、§3.3：exec 整個請求比現況多 4.5 ms（x86）、9.8 ms（ARM），常駐也只有 0.2–0.8 MB。v1 用 exec。

### 13.2 Reclaim 目前用到的 Rust SDK 功能

Reclaim 的 `package/privilege` 依賴 `syno-pkg-land-sdk` 的 git tag v0.9.7。用到的 SDK 模組是 `api`、`dsm`、`http`、`portal`、`ctx`；在 SDK 裡這些約 7,800 行（含測試）。SDK 其他模組（background、delegate、identity、filestation 等）沒用到。

| Rust SDK 功能 | Reclaim 的用法（檔案） | 新架構由誰提供 |
|---|---|---|
| `api::Server::new_with_permissions`、`Route::handle_request`、`set_max_request_bytes(MAX_REQUEST_BYTES_CEILING)`、來源檢查、期限 | CGI 主機：把業務 operation 接成 route（`cgi_host.rs`） | Go runtime CGI（`bridge/cmd/runtime`）。operation 列在 manifest，請求上限也由 manifest 宣告；業務邏輯寫成 Rust 薄層 App 的 method |
| `dsm::Authenticator`、`dsm::new_entry_gate(…, ENTRY_ADMIN_ONLY)` | CGI 的 DSM 登入檢查與只限管理者的入口（`cgi_host.rs`） | Go runtime（`dsm.Authenticator`、`EntryGate`），manifest 宣告入口只限管理者 |
| `http::cgi::serve`、`http::Request`、`http::Recorder` | CGI 入口與測試（`cgi_host.rs`、`bin/cgi.rs`） | Go runtime。App 的測試改用薄層 SDK 的 `App::serve` 對 pipe 測，像 `rust/app/src/tests.rs` |
| `api::dsm_session(request)` → `DsmCredentialContext` | CGI 把瀏覽器的 cookie 與 SynoToken 轉給 Reclaim 自己的 root helper（`cgi_host.rs`） | **與新架構衝突**：App 永遠拿不到 session（§7、§9）。改成 runtime 以 svc `delegate.*` 呼叫 executor，由 runtime 代填 `auth` |
| `dsm::Authenticator`（在 root helper 內） | root helper 執行 root 工作前自己再問一次 DSM（`dsm_session.rs`、`bin/privileged.rs`） | Go executor 已做同樣的事：以 root 收到 `auth` 後自己向 DSM 驗證（`executor/dsm.go` 的 `Verify`） |
| `dsm::set_bridge_fallback_dir` | 沒有 `memfd_create` 的 kernel 上，DSM role bridge 的暫存位置（`cgi_host.rs`、`bin/privileged.rs`） | Go `dsm` 套件與 executor 已處理（`dsm/bridge_file.go`） |
| `portal::config_path`、`portal::read`、`portal::with_entry` | 讀 portal 設定與狀態（`portal.rs`） | svc `portal.status`（Go `portal.Read`），或前端直接呼叫 SDK 路由 `portal.*` |
| `portal::root::{purge, Policy, valid, Entry, Command, Paths}` | 移除套件時以 root 清掉 portal 項目（`portal.rs`、`bin/privileged.rs`） | Go executor 的 portal 與殘留清理（`executor/portal_entry.go`、`purge_leftovers.go`） |
| `ctx::Context::background` | 呼叫 `portal::root::purge` 用（`portal.rs`） | 不再需要 |

### 13.3 Reclaim 遷移要做的事

關鍵是 root 路徑。Reclaim 目前有自己的 root helper 與 helper 協定（`bin/privileged.rs` 約 1,270 行、`bootstrap.rs` 300 行、`lifecycle.rs` 156 行），並由 CGI 把 DSM session 轉給它。新架構下 App 拿不到 session，所以這條路必須改。

| 路線 | 做法 | 估計 | 評估 |
|---|---|---|---|
| **M1（建議）** | CGI 換成 Go runtime＋Rust 薄層 App；Reclaim 的 root 工作改成 SDK executor App 的 root operation 與 policy，經 `delegate.*` svc 呼叫；移除 Reclaim 自己的 helper 協定與 DSM 檢查 | 15–25 人天（信心：中低） | 符合兩層權限：root 能力只屬 executor App，DSM 再驗證由 Go 統一做。工作量取決於 Reclaim 的掃描、刪除、升級、protected 目錄信任等邏輯有多少能對應到 executor 的既有 operation；需要先逐一盤點 |
| M2（過渡） | CGI 換成 Go runtime＋Rust App；runtime 新增「把 session 轉給套件自己的 root helper」的 svc；helper 保留，並把 SDK 的 `dsm` 驗證（約 1,700 行含測試）搬進 Reclaim | 6–10 人天 | 不建議：把 session 交給 App 管轄的通道，違反 §7；而且 Reclaim 裡會留下第二份 DSM 驗證，就是這次要消除的重複 |
| M3（不遷移） | Reclaim 釘在 SDK tag v0.9.7 | 0 | 可以當短期狀態：tag 不會消失，Reclaim 照樣能建置；但拿不到 SDK 之後的安全修正 |

M1 的步驟：

1. 盤點 Reclaim 的 root operation，對照 executor 的 operation 與 root-ops policy，列出缺口（1–2 天）。
2. 在 executor 補上 Reclaim 需要而目前沒有的 root operation 與 policy 欄位（5–10 天，在 SDK repo）。
3. 把 `cgi_host.rs` 的 route 改成 Rust 薄層 App 的 method，`dsm_session` 轉送改成 `ctx.runtime("delegate.…")`（3–5 天）。
4. 打包改成 Go runtime＋executor App＋Rust App；移除 privilege bootstrap 與 helper（2–3 天）。
5. 實機回歸：x86 與 ARM、管理者與一般帳號、升級與移除（3–5 天）。

### 13.4 SDK repo 這一輪的移除步驟（P6 已完成）

1. 刪除 `rust/sdk`（約 2.69 萬行），保留 `rust/app` 薄層（1 天）。
2. gate：拿掉 Rust 三目標 clippy 中的 `syno-pkg-land-sdk`、`scripts/rust-linux-test.sh` 的 SDK 測試、`vector-client` 與 `conformance/rust_client_test.go`；改成檢查薄層（0.5 天）。
3. CHANGELOG 與 README 說明 Rust 完整 SDK 停在最後一個 tag，Reclaim 釘版（0.5 天）。

Reclaim 依賴 git tag，所以 SDK 刪除 Rust 完整 SDK 不會讓 Reclaim 立刻建置失敗；但在 Reclaim 完成 M1 前，它拿不到新的修正（M3 的風險）。

## 14. 分階段計畫與工作量

估計以一位熟悉此 repo 的開發者（或一個 thread）計，含 TDD 與本機 gate，不含實機排程等待。v1 只做 exec，所以 P1 不再包含 socket 併發上限與常駐監控。

| 階段 | 內容 | 工作量 |
|---|---|---|
| P1 Go runtime 正式版（**完成**） | `bridge/cmd/runtime`、manifest、`app_unavailable`、errors 白名單 | 3–5 天 |
| P2 transfer | spool 上傳／下載、與 delegate staging 整合、golden transfer 案例 | 4–5 天 |
| P3 svc 服務表 | files、delegate、bg、portal 的 svc 與測試 | 4–6 天 |
| P4 Python SDK | 正式 API、型別產生（TypedDict）、文件、3.8 相容 CI（容器） | 3–4 天 |
| P5 Node SDK | 正式 API、`.d.ts` 產生、Node 18 相容 CI | 3–4 天 |
| P6 Rust 薄層 SDK | 正式 API、文件、三目標 clippy；移除 `rust/sdk` 與相關 gate（§13.4） | 3–4 天 |
| P7 CLI 打包 | `runtime: python|node|rust`、多 arch runtime、Rust App 交叉編譯、INFO 相依、`--describe` 核對 | 5–7 天（CLI repo） |
| P8 文件與範本 | `native-python`、`native-node`、`native-rust` 範本與 overlay | 3–5 天（templates repo） |
| P9 驗收 | 跨語言 runner 擴充、x86／ARM 實機（安裝 SPK、兩使用者互拒） | 3–5 天 |
| P10 Reclaim 遷移（M1） | §13.3 | 15–25 天（Reclaim repo 與 SDK executor） |

合計：SDK 與工具 P1–P9 約 31–45 人天；加上 Reclaim 遷移約 46–70 人天。
