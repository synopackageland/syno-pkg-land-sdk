# Changelog

## 0.31.3 (2026-10-26)

- Accept UI 0.17.x with the optional UI peer range `>=0.10.1 <0.18.0`; build the Vue SDK against UI 0.17.0 (the redesigned `Wizard` and `WizardDialog`). Apps no longer need a UI peer override for this combination.
- Include the launcher's **Open Package Center** offer and its test cleanup, committed after the v0.31.2 tag.
- Align SDK/App and runtime identifiers at 0.31.3. Reuse immutable executor/v0.31.0 and its existing payload digests; CLI/templates remain at 0.31.0. No Rust crate publication is included.

## 0.31.2 (2026-10-25)

- Redesign the DSM menu launcher page (`open.cgi`) as one DSM-style card: a text-free inline illustration of the browser, the hop that reached DSM and the NAS marks the broken, paused or sign-in hop, animates while `/_spl/ping` is checked and shows success briefly before opening. Light and dark themes, reduced motion and narrow screens are supported. The style is now pinned by hash instead of `unsafe-inline`; no-store, no-referrer, no frames, the hashed probe script and the single `connect-src` origin are unchanged. See [launcher page](docs/external-access.md#launcher-page).
- Check the DSM hostname and App port before opening them when DSM was reached from outside the local network (a domain, DDNS name or global IPv4 address), where a router often forwards only DSM's port. On failure administrators read why and get **Set up this App in Connect**, or **Open Package Center** to install Connect; other users are asked to contact their administrator. Private, loopback, link-local and other non-global addresses, IPv6 literals, single-label names and local suffixes keep the plain `302`. See [DSM menu destination](docs/external-access.md#dsm-menu-destination).
- Align SDK/App and runtime identifiers at 0.31.2. Reuse immutable executor/v0.31.0 and its existing payload digests; CLI/templates remain at 0.31.0. Apps must rebuild with SDK/gateway 0.31.2 to ship the new page. No Rust crate publication is included.

## 0.31.1 (2026-10-25)

- Accept UI 0.16.x with the optional UI peer range `>=0.10.1 <0.17.0`; build the Vue SDK against UI 0.16.0. Apps no longer need a UI peer override for this combination.
- Align SDK/App and runtime identifiers at 0.31.1. Reuse immutable executor/v0.31.0 and its existing payload digests; CLI/templates remain at 0.31.0. No Go module tag or Rust crate publication is included.

## 0.31.0 (2026-10-25)

- Restart native daemons after an unexpected exit by default, with bounded backoff; `restart.mode: never` opts out. Registry checks honor configured Docker mirrors and retain actionable pull errors.
- Preserve SPA route fragments through DSM authorization, so menu launches return to the requested App screen after sign-in.
- Reject ambiguous Compose paths containing `$`, excessive include fan-out and image volume inventories before package admission. Strip identity header aliases, bound OIDC parameters, enforce proxy idle timeouts and validate batch request envelopes. Refused DSM status output no longer discloses response details.
- Disabling a delegated grant stops its scheduled tasks before returning. System-access approval explanations now describe the changed protection consistently.
- Align SDK/App and runtime identifiers with CLI/templates 0.31.0. Ship executor/v0.31.0 with refreshed reproducible payload digests; rebuilt Apps require administrator reapproval for changed protected payloads. Rust crate publication remains deferred. No NAS acceptance is claimed.

## 0.30.2 (2026-10-25)

- Fix DSM main-menu launches reached through an external provider when the top-level navigation carries the DSM session cookie without a SynoToken. The launcher obtains a token from the same-origin DSM endpoint and submits a bounded, origin-checked form before the existing session and App-access checks. Expired sessions remain refused; LAN GET behavior is unchanged.
- Keep CLI/templates 0.30.0 and immutable executor/v0.27.0. Apps must rebuild with SDK/gateway 0.30.2 to ship the fix. No Rust crate publication is included.

## 0.30.1 (2026-10-24)

- Restore the intended Compose `external_origin` + `setting` fallback. The host selects an external origin first, otherwise a validated installation value; with neither, the container environment key stays unset. All services share one snapshot and one fallback policy. Installed preflight revalidates that policy and refreshes the snapshot on restart.
- Keep CLI/templates 0.30.0 and immutable executor/v0.27.0. App rebuilding and DSM acceptance remain separate; no Rust crate publication is included.

## 0.30.0 (2026-10-24)

- `/_spl/ping` answers any origin (`Access-Control-Allow-Origin: *`, no credentials), so the DSM menu launcher can check that an external App address is reachable before opening it. The response is unchanged: only the package name.
- The DSM launcher (`open.cgi`) opens an App at the address that matches the provider the browser used to reach DSM. A small page checks `/_spl/ping` for up to 8 seconds before opening it and otherwise explains the failure with **Open anyway**. When the App is not enabled, paused, behind a QuickConnect relay or single-origin without an external origin, administrators get the reason and a link to the App in NAS Remote Connect; other users are told to ask an administrator. LAN behavior is unchanged. Single-origin Apps now open through the same page instead of a direct `302`. See [launcher page](docs/external-access.md#launcher-page).
- Add `MenuDestination` and `LoadMenuSources`: the DSM menu destination for the host the browser used to reach DSM, from the App's own `dsm_origin`/`manual_origin` pair and Connect's new menu-only launch map (`spl/launch.json`). LAN and unknown hosts keep the DSM hostname and App port. Add `externalorigin.CanonicalWithPort` for private network addresses. See [DSM menu destination](docs/external-access.md#dsm-menu-destination).
- Add `identity_gateway.external_access.single_origin` for Apps whose upstream sign-in works on one fixed URL. While an external origin is set, the DSM launcher opens the primary origin, top-level browser navigations on other hosts are redirected to it, and the settings page states that only that address can sign in, with a tailnet reachability note for `*.ts.net` addresses. Its `manual_origin` may carry an explicit HTTPS port, such as the tailnet address on the App port (`https://<name>.ts.net:<App port>`), so Vaultwarden can use Tailscale; other Apps and the `external-origin` installation setting keep `https://<DNS hostname>` on 443. Without an external origin, behavior is unchanged. See [single-origin Apps](docs/external-access.md#single-origin-apps).

## 0.29.1 (2026-10-24)

- Accept UI 0.15.x with the optional UI peer range `>=0.10.1 <0.16.0` and validate the Vue SDK against UI 0.15.0. Apps can install this combination without `--legacy-peer-deps`.
- Align SDK/App and generated runtime identifiers at 0.29.1. Reuse immutable executor/v0.27.0 and its existing payload digests; executor, lifecycle and canonical task definitions are unchanged. Rust crate publication remains deferred.

## 0.29.0 (2026-10-24)

- Reuse immutable executor/v0.27.0 and its six verified payload digests; executor sources are unchanged since 0.27.0.

- Add external access to the identity gateway. An optional `identity_gateway.external_access` policy (`connect: optional|none`, `origin_change: none|confirm`) lets an App serve a public `https://<DNS hostname>` origin from a manually configured value or a read-only Connect projection. The App keeps its dedicated HTTPS listener and DSM admission policy; the gateway creates no tunnels and changes no DSM settings. See [external access](docs/external-access.md).
- Admit configured external authorities and resume public sign-in with durable revision tokens, so a sign-in started on the public origin returns there after DSM authentication.
- Add a DSM-origin settings page (`sso/settings.cgi`) for `manual_origin` and `dsm_origin`, checked against the DSM session, SynoToken and administrator role on every request, and stored privately in `<StateDir>/external.json`. Add the `external-origin` installation setting type and Compose origin snapshots. See [external access settings](docs/external-access-settings.md).
- With no manual origin and no Connect projection, the gateway behaves as in 0.28.0; this absent-input baseline is covered by tests.

## 0.28.0 (2026-10-24)

- Reuse immutable executor/v0.27.0 and its six verified payload digests; executor sources are unchanged since 0.27.0. Compose Apps should rebuild to pick up the startup and readiness changes below.
- Rebuild the committed Node App SDK `dist/` so its version identifiers match the package (they still read 0.25.0).

- Let slow NAS devices start Apps. The default `compose.startup_timeout_seconds` is now 900 (was 120), and one readiness probe may take 30 seconds instead of the 5-second DSM status budget.
- Do not stop an admitted App after one minute of failed health checks. The supervisor waits as long as the startup budget; meanwhile the gateway shows a localized, self-refreshing starting page (`app_starting` for API clients) and DSM status stays running. A failure after that budget names the phase and duration in `daemon/daemon.log`.
- Raise stricter Compose healthcheck timing, including inherited image healthchecks, to a slow-NAS floor: timeout 30s, retries 3, start_period equal to the startup budget. Health commands and more tolerant values are kept.
- Skip registry checks for images this package already ran, recorded after readiness in `pkgvar/daemon/compose-images.json`. Offline NAS devices no longer show `COMPOSE_IMAGE_UNREACHABLE` on Start or same-image upgrades.
- Add `GET`/`HEAD /_spl/ping` (SDK_PING) to the identity gateway and native ingress. It answers `{"package":"<package id>"}` with `Cache-Control: no-store` before HTTP authority checks, so external-access routing can be diagnosed. It is public metadata, not proof of ownership.

## 0.27.1 (2026-10-23)

- Reuse immutable executor/v0.27.0 and its six verified payload digests; the SDK/App patch changes release metadata and manifest distribution without replacing the protected executor.

- Carry a generated executor manifest in the Go module and frontend npm package. Release checks compare both copies with reproducible executor/lifecycle build pins; compatible SDK minors no longer need a CLI release.

## 0.27.0 (2026-10-23)

- Keep background worker liveness in package-private tmpfs with process identity and freshness checks; stop clears the observation and idle polling leaves durable jobs untouched.
- Preserve exact executor scheduler verification without idle persistence: unchanged verified task inventories stay in place, and DSM scheduler initialization runs over a fresh SQLite backup in a private mount namespace. Unavailable backup or isolation fails verification without a live-write fallback.

- Retire backend HTTP replay of browser DSM sessions. NAS file pickers and transfers use typed same-origin browser WebAPI calls; native authentication retains the original client address.
- Keep destination grants authorized by the existing executor and add owner-bound permission rechecks for an already granted destination. Browser-only Apps do not need an executor. Rebuilt protected executors remain subject to normal approval.
- Migration required for custom file handlers and unused server-only file services. See [DSM session transport](docs/dsm-session-transport.md); App acceptance remains separate from SDK fixture verification.

- Align SDK/App and generated runtime identifiers at 0.27.0 and validate with UI 0.14.8; retain the optional UI peer range `>=0.10.1 <0.15.0`. Publish a new executor/v0.27.0 with refreshed executor/lifecycle digests; Apps using protected executors must rebuild and follow normal administrator approval. Rust crate publication remains deferred.
- Runtime evidence for idle storage is bounded to the DSM 7.4/x86_64 fixture described in [idle storage](docs/idle-storage.md); other supported architectures receive build and ARM qemu checks, not NAS upgrade certification.

## 0.26.0 (2026-10-22)

- Check the package account's selected shared-folder access before Compose startup and show actionable permission guidance in the App dialog. Recheck access when retrying startup.
- Add bounded native daemon restart policies with backoff, restart limits and health reporting; intentional stops do not restart the daemon.
- Align SDK/App and runtime identifiers at 0.26.0, validating against UI 0.14.3 while retaining the optional UI peer range `>=0.10.1 <0.15.0`. Reuse executor/v0.24.0: executor, lifecycle and canonical task definitions are unchanged.
- Known limitations: approval retention still requires unchanged policy, protected programs and task definitions. Rust crate publication remains deferred. No NAS upgrade certification is claimed.

## 0.25.0 (2026-10-22)

- Resume cold DSM sign-in from the classless `open.cgi` URL launcher for OIDC Apps using `oidc.browser`, including native UI entries with a separate admin console. Retain the transaction and declared-port checks.
- Let `AppShell` select the first declared page from the app brand. Apps can override this with `@brand.prevent`; observing `@brand` preserves the default. Requires UI 0.14.3+ for interactive branding.
- Align SDK/App and runtime identifiers at 0.25.0, validating against UI 0.14.3 while retaining the optional UI peer range `>=0.10.1 <0.15.0`. Reuse executor/v0.24.0; no new executor approval is required solely by this release. Approval retention still requires unchanged policy, protected programs and task definitions. Rust crate publication remains deferred.

## 0.24.1 (2026-10-22)

- `AppShell` handles the UI layout’s `brand` action by selecting the first declared page. Apps can override it with `@brand.prevent`; observing `@brand` alone keeps the default. Requires UI 0.14.3+; older supported UI versions retain their static branding.

- Validate with UI 0.14.3 while keeping the optional UI peer range `>=0.10.1 <0.15.0`. Align SDK/App and generated runtime identifiers at 0.24.1; reuse executor/v0.24.0. Rust crate publication remains deferred.

## 0.24.0 (2026-10-22)

- Rebuild Go SDK runtimes, executor and lifecycle with Go 1.26.10, fixing crypto/x509 GO-2026-6809. Pin the same toolchain in all Go modules and build environments.
- Update build and frontend test containers to Node.js 24.22.1 LTS. Raise the Node App SDK runtime requirement to Node.js 22 and replace the EOL Node.js 18 compatibility gate with Node.js 22.23.4. New native-node templates use DSM Node.js_v22.
- Retain optional UI peer support for `>=0.10.1 <0.15.0`, verified with UI 0.14.0. Align SDK/App and generated runtime metadata at 0.24.0; Rust crate publication remains deferred.
- Known limitations: the rebuilt executor and lifecycle payload digests change. Apps upgraded with this release require administrator reapproval of system access. Refresh the CLI digest inventory; previous release digest records remain immutable. No NAS upgrade certification is claimed.

## 0.23.2 (2026-10-22)

- Accept UI 0.14.x with the optional UI peer range `>=0.10.1 <0.15.0` and validate the Vue SDK against UI 0.14.0. Existing SDK component APIs remain unchanged.
- Align SDK/App and generated runtime identifiers at 0.23.2. Reuse executor/v0.20.0 without executor, lifecycle or canonical task changes. Rust crate publication remains deferred.

## 0.23.1 (2026-10-22)

- Return HTTP error statuses for refused HTTP transfers, including origin/session/permission guards, while preserving JSON error envelopes and details. JSON API calls retain their existing HTTP 200 envelopes.
- Remove prepared attachment headers on refusal; never append JSON after a transfer has started. Node, Python and Rust transfers share the same Go runtime behavior and cross-language contract tests.
- Align SDK/App runtime identifiers at 0.23.1. Reuse executor/v0.20.0; executor, lifecycle and canonical task definitions are unchanged. Rust crate publication remains deferred.

## 0.23.0 (2026-10-21)

- Resolve the OIDC browser redirect origin from each verified DSM launch request so IP and alias entry URLs can share one installed App.
- Preserve origin validation and the fixed callback path; no installation-time public-origin override is required for the native browser bridge.
- Align SDK/App and generated runtime identifiers at 0.23.0. Reuse executor/v0.20.0: executor, lifecycle and canonical task definitions are unchanged. Rust crate publication remains deferred.

## 0.22.0 (2026-10-21)

- Add provider-only OIDC with `identity_gateway.upstream_access: public`: protect the issuer through DSM while allowing direct access to the upstream application.
- Add declared `compose.host_udp` listeners for host-network Apps, and a fixed local `identity_gateway.launch_path` for DSM menu entry.
- Add native installation settings and bounded runtime setting environment values for Compose Apps.
- Align SDK/App and generated runtime identifiers at 0.22.0. Reuse executor/v0.20.0: executor, lifecycle and canonical task definitions are unchanged. Approval retention still requires matching policy, root programs and protected runtime readback. Rust crate publication remains deferred.

## 0.21.1 (2026-10-21)

- Add typed AppShell `navigation` forwarding to AppLayout. `top` places App pages in a horizontal bar with UI 0.13.1+, while mobile keeps its drawer; omission preserves the installed UI sidebar default. Keep the optional UI peer range `>=0.10.1 <0.14.0` and test against UI 0.13.1.
- Let the UI size the top navigation bar and page separately instead of applying the SDK's sidebar-only grid row override.
- Align SDK/App metadata and generated runtime identifiers at 0.21.1. Reuse executor/v0.20.0: executor, lifecycle and canonical task definitions are unchanged from 0.21.0. Rust crate publication remains deferred.

## 0.21.0 (2026-10-21)

- Add optional Intel GPU render-node access through `compose.devices.gpu`, using the DSM native video-driver resource and videodriver supplementary GID. Retain nonroot execution, no-new-privileges and cap_drop ALL.
- Fall back to software when no supported GPU is available; never map card nodes or broaden device access.
- Document DSM provider NET_BIND_SERVICE bounding-set behavior and align current installation guidance at 0.21.0.
- Align SDK/App runtime identifiers at 0.21.0. Reuse executor/v0.20.0: executor, lifecycle and canonical task definitions are unchanged. Approval retention still depends on policy, root programs and protected runtime readback. Rust crate publication remains deferred.

## 0.20.0 (2026-10-21)

- Explain executor upgrades using protected runtime readback: distinguish policy permissions, root program bytes and task template changes before administrator reapproval.
- Support native Compose bind mounts selected at installation with `select: install`, including measured shared-folder access and path refresh without changing the upstream image.
- Keep the gateway pending until host health readiness succeeds; return HTTP 403 with actionable guidance when the DSM App privilege denies access.
- Align SDK/App and generated runtime identifiers at 0.20.0; retain compatibility with published UI versions. Rust crate publication remains deferred.
- Known limitations: executor payload bytes change. Executor Apps rebuilt with this SDK require one administrator reapproval after upgrade; refresh the CLI executor/lifecycle digest inventory. Approval explanations are diagnostics and do not grant permission. No NAS upgrade certification is claimed.

## 0.19.1 (2026-10-20)

- Fix delegated `webapi.call` from any caller with a deadline. Since 0.17.5 the Go `delegate.Client` adds `deadline_ms` when its context has a deadline, which every CGI request has, but the executor's `webapi.call` envelope allowlist omitted it, so the call was refused with `INVALID_REQUEST` before admission and audit. The executor now accepts `deadline_ms` on `webapi.call` and bounds the call by it within the approved operation budget and the 10-second WebAPI limit, as for every other op. A contract test drives the production executor through the real Go client under a deadline.
- Widen the frontend SDK's optional `@synopackageland/ui` peer to `>=0.10.1 <0.14.0` and test against UI 0.13.0. No SDK component change was needed; UI 0.13.0's visible default changes apply to Apps that upgrade UI.
- Align SDK/App metadata and generated runtime identifiers at 0.19.1. The executor keeps its root Go SDK dependency at v0.19.0. The executor payload changes: refresh the CLI executor/lifecycle digest inventory before release, and executor Apps rebuilt with this SDK require one administrator reapproval after upgrade. Rust crate publication remains deferred.

## 0.19.0 (2026-10-20)

- Add bounded nonroot OIDC runtime configuration for Compose Apps, listener host-port routing, forwarded-port validation, and reliable readiness admission.
- Align SDK/App and generated runtime identifiers at 0.19.0; Rust crate publication remains deferred.
- Stage policy-pinned root programs before publishing them, and share identical binaries through protected hard links. Preserve each program name, digest, lifecycle/session restrictions and descriptor-based execution; replace inodes safely on upgrade.
- Known limitations: canonical executor boot/control task definitions change. Executor Apps rebuilt with this SDK require one administrator reapproval after upgrade, even with an unchanged policy. Refresh the CLI executor/lifecycle digest inventory before releasing this SDK; neither payload is interchangeable with an App root program. Deduplication covers protected runtime binaries only, within one App; package-volume payloads remain separate, and a start temporarily needs space to stage the new inventory.

## 0.18.0 (2026-10-20)

- Add explicitly enabled native-client passthrough routes with package-private switches and same-UID SIGHUP reload. Disabling a route rejects new requests and cancels its active streams.
- Add bounded structured background-job results, localized phases, and atomic singleton admission through `SubmitSingleton`, with owner-bound access and ctl integration.
- Add the native OIDC browser bridge for DSM App windows, including SPA logout reauthentication and cold-launch resume.
- Align SDK/App metadata and generated runtime identifiers at 0.18.0. Retain the executor's root SDK dependency at v0.17.5 for the unchanged shared administrator provider; executor and lifecycle payloads are unchanged from 0.17.7. Rust crate publication remains deferred.

## 0.17.7 (2026-10-20)

- Use the shared DSM administrator provider for executor roles and gateway admin admission/revalidation, including directory principals; preserve unknown verification results as unavailable.
- Let verified administrators use executor Apps without depending on Application Privileges registration, matching the CGI entry gate.
- Clarify in all nine authorization locales that a DSM administrators group member must open the App and click Authorize in its dialog.
- Pin the root Go SDK at v0.17.5 in the executor module to reuse the unchanged `dsm.IsAdministrator` implementation without a self-referential release dependency. Refresh executor/lifecycle digest inventory and rebuild consumer Apps to ship this fix.
- Include the shared-folder permission API and dialog from main, alongside the AppShell page-surface forwarding prepared in 0.17.6.
- Align SDK/App metadata at 0.17.7. Rust crate metadata remains aligned; no crate publication is part of this patch release.

## 0.17.6 (2026-10-20)

- Add typed AppShell `pageSurface` forwarding to normal and denied PageContent, preserving the installed UI default when omitted. `none` removes page surfaces and horizontal gutters with UI 0.12.5+; older supported UI peers remain accepted and the UI development dependency stays at 0.12.0 until 0.12.5 is published.
- Align SDK/App release metadata and generated runtime version identifiers at 0.17.6. This patch changes no executor or lifecycle sources; release digest verification remains required before publication.

## 0.17.5 (2026-10-20)

- Record exact Compose App data volume names in the planner and lock for honest uninstall reporting without adding executor or root requirements.

- Support DSM-managed HTTPS through a mutually authenticated private Unix socket, preserving gateway admission, client address binding, secure cookies and WebSocket checks without granting the App root access.
- Explain direct gateway connections before opening a port that may show a certificate warning or use unencrypted HTTP, in all nine gateway locales.
- Keep a running App alive through transient Compose health failures while closing new ingress until readiness is confirmed; policy and generation failures still stop immediately.
- Share monotonic lifecycle deadlines across supervisor startup, handoff and shutdown; derive gateway and executor work from generated operation and approved lifecycle budgets.
- Bound bridge interpreter detection and hello within the request allowance, and propagate remaining executor work time with reply reserve.
- Retain pending cleanup state when portal or application privilege cleanup cannot be confirmed, and distinguish accepted lifecycle requests from completed cleanup.
- Increase DSM session recovery to 15 seconds, internal delivery/reap reserve to five seconds where the outer budget permits, and schedule lateness tolerance to 60 seconds without recovery catch-up.

- Align SDK/App metadata at 0.17.5. Executor and lifecycle payloads change; existing executor approvals require one administrator reapproval after upgrading an App rebuilt with this SDK.

## 0.17.4 (2026-10-19)

- Add container `compose.startup_timeout_seconds` (default 120, integer 2–900) to the planner and generated bootstrap environment. The daemon waits the full declared readiness budget before starting the gateway, with a separate five-second handoff margin; native daemon, stop and uninstall limits stay unchanged.
- Report readiness timeout and the last allowlisted phase without upstream URLs, response bodies or credentials.

- Diagnose image-pull failures with bounded, credential-free image and registry checks so Apps can show actionable Repair guidance.
- Align Node, Python and Rust runtime SDK identifiers with release metadata at 0.17.4.

## 0.17.3 (2026-10-19)

- Generate Node and Python runtime SDK identifiers from release metadata, and reject stale generated constants or mixed language versions before the release gate. Node builds and tests regenerate the constants automatically.
- Align Rust package metadata at 0.17.3 so its compile-time SDK identifier matches the released git tag. Rust crate publication remains deferred.
- Keep the executor and lifecycle protocol version independent of SDK release versions.

## 0.17.2 (2026-10-19)

- Rename password labels and approval instructions to “User password” across all nine locales.

## 0.17.1 (2026-10-19)

- Remove environment-specific release notes and source comments from distributed content.
- Prepare npm releases from a clean package directory without local archive references.

## 0.17.0 (2026-10-19)

- Add AppShell `loading` so the application chrome is visible while its first reads run.

- Fix directory users being rejected by the executor when Core.User omits `expired`. Read native NSS identity, account expiry and uncached administrator membership for every request; verify name/UID in both directions and require a live AD domain controller. Unknown providers and lookup failures refuse.
- Batch up to eight independent JSON methods in one api.cgi request, with per-method authorization/results, one bridged App process, and a bounded executor verification scope for root operations. Add Context.batch and createClient().batch; preserve single calls and existing root concurrency.

- Align TypeScript, Node, Python and Rust SDK versions at 0.17.0. Executor payloads change; rebuild Apps to ship the corrected executor. Rust crate publication remains deferred.

## 0.16.0 (2026-10-19)

- Add `sharedfolder` (Go): the install-time Shared Folder's resolved path and the package account's measured access (none, read, write) against the access the App declared, as `ready` or `pending` (waiting for an administrator's grant); `Require` gives a daemon the path or a `PermissionError` that names what to grant, `Sync` applies a changed path (share moved, another folder chosen) to the App's own storage once, and `APIError` maps these to stable API error codes. Nothing runs as root.
- Add `SharedFolderSettings` (Vue), `createSharedFolderAccess` and `sharedFolderAccessFor` (TS): per-folder state, a notice only when a folder needs attention, the Control Panel steps and a button opening it, and a one-click grant that sets the package account in the administrator's own DSM session where the window provides `ctx.webApi`; only the backend's readback proves a grant. The one-click path has not been run on a NAS. Document the end-to-end flow in docs/shared-folder.md.
- Add Container Manager preflight and structured readiness errors before preparing Compose applications.
- Add `compose.storage_init`: initialize declared writable paths as the package UID while keeping the official image unchanged; the CLI packages the matching architecture initializer.
- Recheck gateway WebSocket admission and close revoked sessions; provide clear gateway denial pages and document trusted HTTPS ingress.
- Remove internal identifiers from source, documentation and published content; gates scan source and actual npm tarballs, with optional external denylist rules.
- Include the built Node adapter in the Go release source so the CLI can package Node Apps from a tagged SDK.
- Align the TypeScript, Node, Python and Rust SDK versions at 0.16.0. All six executor/lifecycle digests match 0.15.2 (Go 1.26.9); this release adds no administrator reapproval requirement for an unchanged policy.

繁中摘要：新增 Go `sharedfolder`：回報安裝時所選共用資料夾的實際路徑與套件帳號的實測權限（無／唯讀／讀寫），與 App 宣告需求比對成「就緒／待授權」；daemon 用 `Require` 取得路徑或得到說明要授權什麼的 `PermissionError`，`Sync` 在改選或搬移 volume 後只套用一次新路徑到 App 自己的儲存設定。全程不使用 root。新增 Vue `SharedFolderSettings` 與 TS `createSharedFolderAccess`／`sharedFolderAccessFor`：逐資料夾顯示狀態、僅在有問題時提示、控制台步驟與開啟按鈕；視窗提供 `ctx.webApi` 時，管理員可在自己的 DSM session 一鍵授權，並以後端實測結果為準（一鍵授權尚未在真機執行）。Container Manager 預檢會先回報可操作的拒絕原因；Compose 可宣告儲存初始化，保持官方 image 不變。gateway 加入 WebSocket 撤權處理與更清楚的拒絕頁面。四語言 SDK 同步 0.16.0；三架構 executor／lifecycle digest 均與 0.15.2 相同；policy 不變時不新增管理員重新核准要求。

## 0.15.2 (2026-10-18)

- Use UI tokens for SDK components and shorten built-in copy across nine locales (SDK-6).
- Accept UI `>=0.10.1 <0.13.0`, test against UI 0.12.0, and update jsdom to 29.1.1 for CSSStyleDeclaration iteration (SDK-7).
- Generate Go and TypeScript delegate DTOs from one schema; reject incompatible Go overrides and detect contract drift (SDK-5).
- Keep TypeScript, Node, Python and Rust SDK versions aligned at 0.15.2. All three executor and all three lifecycle digests match 0.15.1 (Go 1.26.9); this release adds no administrator reapproval requirement when upgrading from 0.15.1. Apps upgrading from 0.15.0 still require the one-time approval introduced in 0.15.1.

繁中摘要：元件改用 UI token，精簡九語系文字；UI peer 接受 0.12.x，並以 UI 0.12.0 測試。Go／TS delegate DTO 改由共用 schema 生成，加入契約漂移與不相容型別檢查。四語言 SDK 同步 0.15.2；Go 1.26.9 下三架構 executor／lifecycle digest 均與 0.15.1 相同，從 0.15.1 升級不新增重新核准要求；從 0.15.0 升級仍需完成 0.15.1 引入的一次管理員核准。

## 0.15.1 (2026-10-18)

- Include the package name in control.sh refusal logs and remove staged `.executor` / `.policy.json` copies after a refusal.
- Document ctl writeOnly privacy and resource/verb naming recommendations; effects remain explicitly declared.
- Keep TypeScript, Node, Python and Rust SDK versions aligned at 0.15.1. Executor task definitions change: Apps rebuilt with SDK 0.15.1 require one administrator reapproval after upgrade. The release record contains the three-architecture executor/lifecycle digests.

繁中摘要：control.sh 拒絕執行時，log 帶套件名並清除 runtime 的 `.executor`／`.policy.json`；補充 ctl writeOnly 隱私與 resource／verb 命名文件。四語言 SDK 同步 0.15.1。**task 定義改變：App 使用新 SDK 重建並升級後，管理員需重新核准一次。** 三架構 executor／lifecycle digest 與重現性結果記錄於 release metadata。

## 0.15.0 (2026-10-18)

- Add the shared Compose planner, no-publish fixed-IP ingress and package-account lifecycle/readiness contract. Upstream Compose bytes remain unchanged; DSM compatibility remains fixture-specific.
- Add the authenticated Syno Package Land API description v1 (`synopkgland-api.json`) and explicit command metadata for Go, Node, Python and Rust Apps, consumed by `synopkgland ctl`.
- Breaking: remove the legacy Compose packaging route and bridge publish mode. Migrate to schema 2 `dsm-package.yaml`, ordered `compose.files`, `compose.web` with container port/health and pinned `identity_gateway.sdk: 0.15.0`; remove `integration.compose`/`integration.identity_gateway`. `web.host_port` is accepted but ignored. Rebuild with CLI/templates 0.15.0 and test install/upgrade; historical `.sc` registrations need separately authorized cleanup. Subnet candidate changes require a clean installation; automatic network migration is unavailable.
- Keep TypeScript, Node, Python and Rust SDK versions aligned at 0.15.0. Executor/lifecycle compatibility is recorded after the full release gate.

繁中摘要：新增統一 Compose planner、不 publish port 的固定 IP ingress，以及四語言共用的「Syno Package Land API 描述」v1。**不相容變更**：移除舊 Compose 打包路線與 bridge publish 模式；遷移至 schema 2 `dsm-package.yaml` 的 `compose.files`、`compose.web`（container port／health）與 `identity_gateway.sdk: 0.15.0`，移除舊 `integration.compose`／`integration.identity_gateway`，以同版 CLI／templates 重建並驗證安裝與升級。`web.host_port` 僅解析相容、會忽略；歷史 `.sc` 不會自動清除，子網 candidate 變更僅供全新安裝。Shared Folder／private bind 等未支援範圍維持明確拒絕。

## 0.14.1

- 同步 0.14.1 發版；API 與 executor/lifecycle 程式不變。

## 0.14.0

- 配合 CLI 的 ui.launch 與模板新分頁預設同步發版；SDK API 與 executor/lifecycle 程式不變。

## 0.13.1 (2026-10-17)

- Accept UI `>=0.10.1 <0.12.0` and test the Vue SDK against UI 0.11.0, including its DataGrid flex columns, filler column and local paging behavior.
- Keep TypeScript, Node, Python and Rust SDK versions aligned at 0.13.1. Executor/lifecycle compatibility is verified by the full release gate.

繁中摘要：Vue SDK 的 UI peer 放寬為 `>=0.10.1 <0.12.0`，允許 App 升級 UI 0.11.0，並以新版 DataGrid 驗證既有檔案瀏覽流程。多語言版本同步至 0.13.1；executor／lifecycle digest 由完整發版 gate 核對。

## 0.13.0 (2026-10-17)

- Add `idgateway`, a package-local DSM identity gateway for existing web applications: embedded OpenID Connect, header and token modes, DSM-origin authorization, supervised application processes and HTTP/HTTPS listeners.
- Track signed-in DSM UIDs, revoke account sessions and run declared account-delete actions. Watch local account deletion through `/etc/passwd` without root; list domain accounts for explicit purge. Account status reports pending deletion retries with bounded backoff; completed deletions are logged.
- The Vue SDK is tested against UI 0.10.5; its peer range remains `^0.10.1`. UI choices use 18 px boxes, a 9 px label gap and 28 px vertical rows; horizontal RadioGroup spacing is 34 px.
- SDK language versions move together to 0.13.0. Executor/lifecycle digest compatibility is recorded in the release artifact after the full gate.

繁中摘要：新增每包自帶的 `idgateway`，讓既有 Web App 以 DSM 身分登入，支援內嵌 OIDC、header／token 模式、DSM-origin 授權、HTTP／HTTPS 與 App 行程監督。以 UID 記錄登入帳號、撤銷 session 並執行宣告的帳號刪除動作；無 root 監看本機 passwd 刪除，網域帳號由管理員明確 purge。狀態可查待重試刪除與退避時間，完成後留紀錄。多語言版本同步至 0.13.0，Vue 以 UI 0.10.5 測試（peer 維持 ^0.10.1），選項框 18px、文字間距 9px、直向列距 28px、橫向 RadioGroup 間距 34px。

## 0.12.2 (2026-10-16)

- `Context.launchParams` and `Context.onLaunch(fn)` type the launch parameters the CLI's DSM window forwards: the App's string values from a desktop URL's `launchParam` or `SYNO.SDS.AppLaunch`, for the launch that opened the window and for each later launch of the open window. Both are optional; older CLIs and `createStandaloneContext` leave them out.
- `launchRoute(params)` reads the `route` convention, an App path such as `/a/notes/42` (one leading `/`, no backslash or control character, at most 2048 characters). `followLaunchRoute(ctx, navigate)` navigates to the opening launch's route and to each later one's, so a DSM link and a portal `#` link reach the same router. `dsmLaunchURL(origin, packageId, params)` builds the desktop link with the double encoding DSM decodes.
- The Vue SDK is tested against UI 0.10.3; its peer range stays `^0.10.1`.
- Executor and lifecycle digests are unchanged from 0.12.1 for all three architectures.

繁中摘要：`Context` 新增選用的 `launchParams` 與 `onLaunch(fn)`，對應 CLI DSM 視窗轉來的啟動參數（桌面 URL `launchParam` 或 `SYNO.SDS.AppLaunch`）。`launchRoute` 讀取 `route` 慣例（以單一 `/` 開頭、無反斜線與控制字元、最多 2048 字元），`followLaunchRoute` 讓 DSM 連結與 portal `#` 連結導向同一個 router，`dsmLaunchURL` 依 DSM 解碼方式產生桌面連結。Vue SDK 以 UI 0.10.3 測試，peer 範圍維持 `^0.10.1`；三架構 executor／lifecycle digest 與 0.12.1 相同。

## 0.12.1 (2026-10-16)

- Profiles explicitly opt into raw WebAPI with `raw_max_bytes` (up to 64 MiB). `response_type:"raw"` streams a bounded CGI response as the consenting user's runner, strips headers, refuses redirects, and returns content type, size and SHA-256. Up to 32 KiB stays inline as base64; larger bodies use App-private staging. Over-limit responses fail without truncated content.
- Go `JobContext.WebAPI().CallRaw` verifies and consumes both transports, deleting staged output. The MailPlus v10 adapter captures original MIME through a fixed inherited body FD, separately from the runner JSON control reply. Caller-supplied outfile is refused. Bridge Apps use the same raw wire result through `webapi.call`. Profile changes require administrator approval and fresh consent; existing JSON profiles preserve their digest and behavior. Approval details show the raw byte limit.
- Executor digests change on all three architectures; lifecycle digests stay unchanged. CLI 0.12.1 is required for raw profiles and released executor digests. UI compatibility stays on the 0.12 line.

繁中摘要：具名 profile 以 raw_max_bytes 明確啟用原始回應；小內容使用 base64，大內容使用 App 私有 staging，保留 content-type、大小與 SHA-256。限制 CGI headers、拒絕 redirect，超限不回截斷內容。Go CallRaw 驗證後立即刪除 staging；既有 JSON profile digest 不變，啟用／變更 raw 上限須重新核准及授權。三架構 executor digest 更新，lifecycle 不變；保留 0.12 的 UI 相容性。

## 0.12.0 (2026-10-16)

- The Vue SDK requires UI `^0.10.1` and is tested against UI 0.10.1.
- `AuthorizationGate`, `AuthorizationSettings`, `PortalSettings` and the executor notice dialog size to their content between the UI Dialog minimum and maximum widths. `NasPicker` keeps its 800px width.
- `AppShell` notices and the executor notice leave their spacing to `PageContent`, which stacks its children at the UI section gap.
- Executor and lifecycle digests are unchanged from 0.11.0 for all three architectures.

繁中摘要：Vue SDK 的 UI peer 改為 `^0.10.1`，並以 0.10.1 測試。授權、入口設定與 executor 通知對話框改依內容決定寬度（介於 UI Dialog 最小與最大寬度之間），NasPicker 維持 800px。AppShell 通知與 executor 通知不再自帶外距，改由 PageContent 的 section gap 排列。三架構 executor／lifecycle digest 與 0.11.0 相同。

## 0.11.0 (2026-10-16)

- Executor `webapi.call` runs exact, named policy profiles as a consenting user's runner, with job-bound or until-revoked grants. Profiles carry purpose text and API/method/version lists in the existing policy digest and administrator approval. Each call checks the profile pin, current account/UID, AppPriv and original client address; revoke and the integration disable switch fence admission and drain active calls.
- Permanently deny authentication, Core/system, FileStation, account/permission/package administration and batch dispatch, including reserved runner/session/routing params. Bound JSON params to 32 KiB, replies to 48 KiB and execution/revalidation to 10 seconds. Audit selectors and results without recording payloads. Return full DSM envelopes; `WEBAPI_UNCERTAIN` is nonretryable and requires outcome reconciliation.
- Go adds `delegate.CreateWebAPIGrant` and `JobContext.WebAPI().Call`. The protocol and generated TypeScript interface include profile grants and `webapi.call`. Node/TypeScript, Python and Rust Apps use the bridge's `webapi.grant/grants/revoke/call` services with capability `webapi:true`. The approval panel shows supplied profile purposes and exact API versions.
- DSM 7.3.2 verified Calendar own-event CRUD and cross-user denial through a real executor socket from the package account with no session, plus grant ownership and revocation. Runner probes also verified MailPlus reads and Drive metadata; Office streaming exports are excluded from this JSON operation. Other package APIs require their own compatibility probe.
- All three executor digests change; lifecycle digests are unchanged. Existing policies retain approval through the INFO anchor. Adding/changing profiles changes policy and requires administrator approval and user profile consent. CLI 0.11.0 adds manifest profiles and the digest table; downstream apps upgrade backend/frontend together.

繁中摘要：新增以使用者授權身分在背景呼叫 DSM JSON WebAPI 的具名 profile；用途、API／method／version 清單納入管理員核准與 digest，每次重驗帳號、AppPriv、原始 IP 與授權。支援限時或撤銷前有效、撤銷及全域整合停用；永久阻擋登入、Core、FileStation、權限／套件管理與批次轉送，限制參數／回覆大小與逾時並記錄稽核。不自動重試，結果不確定回 WEBAPI_UNCERTAIN。Go 與多語言 bridge 介面、協定／文件同步。已驗證套件帳號無 session 背景執行 Calendar 與跨使用者拒絕；Office 串流匯出不在首版 JSON 範圍。三架構 executor digest 更新，lifecycle 不變；新增／修改 profile 須重核准，搭配 CLI 0.11.0。

## 0.10.12 (2026-10-15)

- `PageInfo` places a page's notes behind an information button beside the `AppShell` title. It supports `inline`, an accessible `label`, custom slot content and localized note counts. `PageInfoTarget` is available for custom shells and fixtures.
- `PortalSettings` confirms before removing the entry serving the current Portal page, then shows how to reopen the App from the DSM desktop and enable the entry again.
- The Vue SDK requires UI `^0.9.10` and is tested against UI 0.9.10, including InfoTip alignment, the info icon and App window collision boundaries.
- Gate scripts install locked dependencies before an unpublished UI tarball and preserve a caller's `VITEST_MAX_WORKERS` setting.
- Executor and lifecycle digests are unchanged from 0.10.11 for all three architectures.

繁中摘要：PageInfo 在標題旁提供附註入口，支援 inline、label、自訂 slot 與多語系；提供 PageInfoTarget。從入口頁移除自己的入口前先確認，移除後指引從 DSM 桌面重新開啟與啟用。UI peer 與測試版本更新至 0.9.10，修正 gate 安裝順序與 worker 設定；三架構 executor／lifecycle digest 不變。

## 0.10.11 (2026-10-15)

- `AppShell` hides its duplicate brand and default account controls while DSM's Application Portal banner is visible. The fallback shell follows banner visibility through the shell lifecycle; custom account slots and supported web-layout footers retain their behavior.
- Bridge App runtimes expose the fixed `portal.register` and `portal.unregister` routes for executor-managed entries. Entry registration rollback uses its own bounded deadline so cleanup can finish after the registration request expires.
- Executor digests change for all three architectures. An unchanged policy and a matching root-owned INFO anchor preserve an existing approval; policy or capability changes require administrator approval.

繁中摘要：DSM Application Portal banner 顯示時，AppShell 隱藏重複的品牌與預設帳號區；保留自訂帳號 slot 與 web layout footer 的契約。bridge App 提供固定的入口註冊／移除路由，註冊失敗後以獨立期限完成 rollback。三架構 executor digest 更新；policy 不變且符合 root-owned INFO anchor 時沿用核准。

## 0.10.10 (2026-10-15)

- Executor and standalone identity session validation use DSM's native `authenticate.cgi`, with the browser's original client address, cookie and SynoToken. The executor requires all three carriers; session checks do not replay the browser cookie through a loopback HTTP request.
- Portal windows without a JavaScript SynoToken obtain it through one shared same-origin `GET webman/login.cgi?enable_syno_token=yes` request. The helper has a five-second deadline, rejects redirects and invalid responses, keeps the token in memory and reads updated desktop tokens. API and executor validation remain enforced. The CLI integrates the helper before backend and DSM WebAPI calls.
- `ExecutorApproval`, `ExecutorNotice` and `ExecutorSettings` accept `purpose` to explain the app's use of system privileges. Approval uses a single consent card: an administrator sees the reason, password field and Approve button on one row, with details in a collapsed “About this approval” section. Ordinary users see instructions to ask an administrator. Password errors appear at the field and retain the entered value for retry. Approval details explain declared tasks and removal through uninstall. `AppShell`'s built-in notice uses the default purpose; custom purposes require an explicitly rendered notice or approval component.
- `BackgroundSaveSettings` shows one save-folder row per destination key. For install-selected scopes, an administrator confirms the scope and grants the same root as their destination in one submission; ordinary users pick their own folder inside the approved scope. The settings component exposes destination selection and revocation. Applications manage save history, cancellation and app-wide disable operations through the existing headless API. `BackgroundSavePanel` offers an opt-in sample producer with `sampleSave`; `BackgroundSaveSettings` exposes its default slot and removes the `operations` slot.
- Entry refusals give concrete instructions in nine locales: `app_privilege_denied` directs an administrator to Control Panel > Application Privileges > Edit for the app; `admin_required` asks for an administrator account. Other refusal codes retain their own messages.
- i18n catalogs remove `hintPassword`, `statusNeeds`, `executorNeeds`, `scopeNeeds`, `consentOnce` and `statusScope`. Save `approveHelp` describes the folder field. Unused save-history and disable-control keys are removed from the bundled catalogs; consumers with custom catalogs must use the current component keys.
- The Vue SDK is tested against UI 0.9.9; its UI peer range is `^0.9.5`. Executor digests are pinned for all three architectures. An unchanged policy and a matching root-owned INFO anchor preserve an existing approval; policy or capability changes require administrator approval.

繁中摘要：executor 與獨立登入以 DSM 原生 authenticate.cgi 驗證並保留瀏覽器 IP；portal 缺少 SynoToken 時，前端先以同源請求取得並暫存於記憶體，維持 API／executor 驗證。核准畫面以一次性 consent 卡片說明用途，細節收合；`ExecutorApproval`、`ExecutorNotice`、`ExecutorSettings` 提供 `purpose`，密碼錯誤在欄位下顯示並保留輸入供重試。背景存檔每個 key 使用一列存放資料夾，管理員一次完成範圍核准與自己的目的地授權；工作紀錄、取消與全 App 停用由 App 經 headless API 管理。`operations` slot 與上述舊 i18n key 移除。入口拒絕提供具體處理步驟，UI 測試版本為 0.9.9。

## 0.10.9 (2026-10-15)

With no `ExecutorSetup` provided, `ExecutorApproval` shows the App's pages
alone, with or without `preload` (Reclaim's P2 tests). The executor
and lifecycle digests equal 0.10.8. An executor upgrade keeps the
existing approval when the policy is unchanged and the new executor matches
its root-owned INFO anchor. A policy or capability change requires an
administrator's approval again.

ts/README.md adds: when the status is not ready, `useExecutorPreload()` turns
`false` before the pages unmount, so `false` does not mean they show; open a
teleported dialog from `watchPostEffect` (or after the next render).

繁中摘要：沒有 ExecutorSetup 時，只顯示 App 頁面，不保留「載入中」文字。executor 與 lifecycle
digest 與 0.10.8 相同；policy 不變且新 executor 符合 root-owned INFO anchor 時，升級沿用原核准；
policy／能力改變才需管理員重新核准。README 補充：status 不是 ready 時，`useExecutorPreload()` 會在頁面卸載前就變
false，要開 teleport 對話框請用 `watchPostEffect`。

## 0.10.8 (2026-10-15)

The executor reports its own scheduler readback for the first open.
Its digests differ from 0.10.7; the lifecycle digests match 0.10.7. An
executor upgrade keeps the existing approval when the policy is unchanged
and the new executor matches its root-owned INFO anchor. A policy or
capability change requires an administrator's approval again. The Vue entry is tested
against UI 0.9.8; its peer range stays `^0.9.5`.

Incompatible: none in the API. `status` gains the field `tasks`; every
reader in the SDK and the CLI parses `status` leniently, and Go
`delegate.ExecutorRoutes` passes it on as raw JSON. Mixed versions open as
they did before (measured in the test harness: 11.4 s on a loaded NAS, three
scheduler API calls): CLI 0.10.8 ignores `tasks` from a 0.10.8 executor, and
a DSM window from a newer CLI does its full readback for a 0.10.7 executor,
which answers no `tasks`. Only a 0.10.8 executor with a CLI that reads `tasks`
skips the window's scheduler readback. `preload` is opt-in: without it,
`ExecutorApproval` renders as in 0.10.7.

繁中摘要：executor digest 改變（lifecycle digest 不變）；policy 不變且新 executor 符合 root-owned
INFO anchor 時，升級沿用原核准；policy／能力改變才需管理員重新核准。`status` 新增 `tasks` 欄位，SDK 與 CLI 都以寬鬆方式解析：舊 CLI＋新 executor、
新 CLI＋舊 executor 都和改前一樣（完整核對）。`preload` 是 opt-in，不加則行為不變。

Additions:

- `ExecutorApproval` takes `preload`: the App's pages mount, hidden and inert,
  while the first executor status is read, so their reads run alongside it.
  A ready status shows them with what they loaded; any other discards them.
  Without `preload` nothing changes. `useExecutorPreload()` says whether the
  pages are being preloaded; `AuthorizationGate` opens its dialog only after.
  繁中：`ExecutorApproval` 新增 `preload`：核對期間先隱藏掛載 App 頁面並預讀資料，
  通過才顯示，失敗就丟棄。

Behavior changes:

- The executor reads its approval tasks back every 10 minutes while it runs
  (every minute after a failure), not only at start, and `status` reports the
  verdict as `tasks: {state, checked_at}` (`checking`, `ready`, `stale`,
  `failed`). A DSM window from a CLI that reads it shows an administrator the
  App without its own scheduler readback (spec/scheduler/v1). The existing
  approval covers an INFO-anchored executor upgrade under the unchanged policy.
  繁中：executor 每 10 分鐘重新核對排程任務，`status` 回報 `tasks`；policy 不變且
  新 executor 符合 root-owned INFO anchor 時，升級沿用原核准。
- The protocol spec says what CLI 0.10.8's DSM window does with a refusal
  that sent nothing: it shows `APP_STOPPED` at once and repeats `APP_STARTING`
  and `EXECUTOR_STARTING` for 4 s. No SDK code changed: the SDK's panels and
  Gate already show a stopped or starting package and check again on their own.
  繁中：協定規格更新：瀏覽器端立即顯示 `APP_STOPPED`，啟動中的碼只短暫重試。

## 0.10.7 (2026-10-15)

Fixes from the nas.example runs of Reclaim 1562 and Download Hub 0031.
The executor source did not change, so the executor and lifecycle
digests stay as in 0.10.6. `delegate` changed, but it runs in the package
account (the App's backend and background worker), not in the executor or a
root program; an App rebuilt on 0.10.7 is approved again only when its
executor or its policy (which names each root program's digest) changes, as
in 0.10.6. The Vue entry is tested against UI 0.9.8; its peer range stays
`^0.9.5`.

Incompatible: none in the API. Behavior that an App's own tests may see:
`ExecutorApproval` and `ExecutorSettings` show the lifecycle text instead of
the approval form while the package is stopped or starting; `BackgroundPanel`,
`FilesPage` and `SaveOperations` show it instead of an error with a refresh
button; an action a stopped or starting package refuses reads "This app is
stopped. Start it in Package Center, then try again." An App that turns a
lifecycle code into text on its own error path should use
`lifecycleRefusal(error, locale)` (or `lifecycleCopy(l, locale, {for:
'action'})`) for an action and `lifecycleCopy` for a page that polls.
`PortalSettings` starts without placeholder rows only in a window whose
`ctx.portal` has `lastStatus()`, which CLI 0.10.7 generates.

繁中摘要：executor 原始碼未改，executor 與 lifecycle digest 與 0.10.6 相同；`delegate`
有改，但它只在套件帳號（App 後端、background worker）執行，不在 executor 或 root 程式內，
所以只有 executor 或 policy（含 root 程式 digest）改變的 App 才要重新核准。套件停止或啟動中時，
`ExecutorApproval`／`ExecutorSettings` 不再顯示核准表單，改顯示「已停止／啟動中」並自行重試；
`BackgroundPanel`、`FilesPage`、`SaveOperations` 的載入也一樣。動作被拒時用
`lifecycleRefusal` 或 `lifecycleCopy(..., {for: 'action'})` 的「請啟動後再試一次」文案。
`PortalSettings` 需要 CLI 0.10.7 產生的視窗（`ctx.portal.lastStatus()`）才不畫佔位列。

Additions:

- `lifecycleCopy(lifecycle, locale, {for: 'action'})` words an action that a
  stopped or starting package refused, which nothing sends again: "This app
  is stopped. Start it in Package Center, then try again." and "This app is
  starting. Wait a moment, then try again." (9 locales; `LifecycleCopyOptions`).
  The default (`for: 'page'`) keeps "this page updates by itself", for a view
  that polls again; ts/README.md says when to use which (a download
  refused while stopped stayed in the dialog under the page text).
- `lifecycleRefusal(error, locale)`: the action text for a refusal that names
  a stopped or starting package, else `undefined`. `saveError`,
  `nasErrorCopy` and `backgroundErrorCopy` take an optional last `locale`;
  with it, such a refusal reads as that action text instead of the generic
  error. The SDK's own panels (`DelegatePanel`, `BackgroundPanel`,
  `BackgroundSaveSettings`, `SaveOperations`, `NasPicker`, `FilesPage`,
  `FileBrowser`) pass it.
- `LIFECYCLE_STARTING_RETRY_MAX_MS` and `lifecycleRetryDelay(attempt,
  lifecycle)`; see below.
- `PortalAccess.lastStatus()` (optional): the newest status the access read.
  `createPortalAccess` reads the status once when it is created, and a
  `status()` asked for while a read runs shares it.

Behavior changes:

- `delegate.Client` with a `Mailbox` (so `delegate.Local` and the bridge
  runtime's client) refuses with `APP_STOPPED` before it dials when the
  mailbox asks the package to stop. While a package stops, its executor can
  hold the socket without answering, and a call waited out its timeout:
  Download Hub showed its stop text 28 s after the stop.
- `ExecutorApproval` and `ExecutorSettings` show "This app is stopped" or
  "This app is starting" for a status whose code or reason names
  `APP_STOPPED`, `APP_STARTING`, `EXECUTOR_STARTING` or `executor_starting`,
  with no password form, and check again on their own until the executor
  answers. They showed the approval form with "APP_STOPPED: the executor
  status request failed." and never checked again. A status read
  that ends in the window's `executor_starting` now says the package is
  starting instead of "still running its start step"; an approval that ends
  there still explains it in its alert.
- A panel that shows a stopped or starting package checks a starting one at
  most 4 s apart (`lifecycleRetryDelay(attempt, 'starting')`,
  `LIFECYCLE_STARTING_RETRY_MAX_MS`), and starts its checks over when
  stopped turns to starting. A package stopped for a minute, whose start step
  takes 15.5 s, shows again within 20 s of its start, wherever the start falls
  between two checks; the checks had stayed 15 s apart (up to 28 s).
  `AuthorizationGate`, `ExecutorNotice`, `ExecutorApproval`,
  `ExecutorSettings`, `DelegatePanel` and `BackgroundSaveSettings` check this
  way. A stopped package is still checked after 2, 4, 8 and then every 15 s.
- `BackgroundPanel`, `FilesPage` and `SaveOperations` say "This app is
  stopped" or "This app is starting" when a stopped or starting package
  refuses their load, and load again the same way until it answers; they
  showed the refusal as an error with a refresh button. An action such a
  package refuses still reads as the action text.
- `PortalSettings` starts as the access's `lastStatus()` and reads the
  status again; with none it keeps the placeholder rows. Reclaim's settings
  page moved 100 px when the three placeholder rows became the Control Panel
  button DSM leaves an administrator whose App it does not list. On
  the SDK fixture of that page the content under the section now moves 0 px.

## 0.10.6 (2026-10-15)

Fixes from Reclaim's NAS e2e run and the settings-page CLS checks.
The executor source did not change, so the executor and lifecycle
digests stay as in 0.10.5. An App rebuilt on 0.10.6 is approved again only
when its executor or its policy (which names each root program's digest)
changes; an App whose root programs do not embed this SDK keeps its
approval. The Vue entry is tested against UI 0.9.8; its peer range stays
`^0.9.5`.

Incompatible: none in the API. Behavior that an App's own tests may see:
`credential_not_ready` (a new code) where `session_required` was, for a DSM
session without a SynoToken; `executor.status` and `delegate.status` refuse
with `APP_STOPPED` or `APP_STARTING` while the package is stopped or
starting; `delegate.Local` reads the lifecycle mailbox by default; every SDK
settings section loads with `[data-placeholder-row]` rows, not
`[data-ui-skeleton-line]`.

Correction to 0.10.4 and 0.10.5: those notes said a root program built with
this SDK (naming Reclaim's) gets a new digest and is approved again after the
upgrade. Approval pins the executor digest and the policy digest; the policy
names each root program's digest, and the executor checks a root program's
bytes against it before every run. So an App is approved again exactly when
its executor (outside the INFO anchor) or a root program's bytes change, and
a changed root program can never run under the old approval. Reclaim's root
program does not use `syno-pkg-land-app` (its package-account `app` binary
does), so its bytes, and the policy, were the same in 1560 and 1561, and
Reclaim kept its approval.

繁中摘要：更正 0.10.4、0.10.5 的說明：核准綁 executor digest 與 policy digest，policy
內含每個 root 程式的 digest，executor 每次執行 root 程式前都比對；所以只有 executor（INFO
anchor 以外）或 root 程式位元組改變才要重新核准，換掉的 root 程式不能沿用舊核准執行。
Reclaim 的 root 程式不含 `syno-pkg-land-app`（只有套件帳號的 `app` 用），1560／1561
位元組與 policy 相同，因此不需重新核准。`ExecutorSettings` 首次載入改顯示最終的狀態列，不再是三條
skeleton line（設定頁 CLS）；`DelegatePanel`、`BackgroundPanel` 也一樣，SDK 所有設定區塊首次載入都畫最終列（`[data-placeholder-row]`）。`DelegatePanel` 收到 `APP_STOPPED`／`APP_STARTING`／
`EXECUTOR_STARTING` 時顯示「App 已停止／啟動中」並自行重試，不再顯示錯誤。`@synopackageland/sdk` 匯出
`appLifecycle`、`lifecycleCopy`（9 語系）與重試間隔，App 自己的輪詢錯誤可交給 SDK 判斷與顯示。Go 的 `delegate.Local` 預設讀套件 lifecycle mailbox，bridge runtime
的 executor client 也都讀，App 不必自己設 `Mailbox`；直接建 `&delegate.Client{}` 的仍不讀。套件停止時 `executor.status` 改回 `APP_STOPPED`、剛啟動 10 秒內回
`APP_STARTING`（與 `root.*` 相同），DSM 視窗與 SDK 面板因此顯示「已停止／啟動中」並
自行重試，不再顯示核准表單。有 DSM session cookie 卻沒有 SynoToken 的請求（從 profile
還原的 session、Application Portal 頁面）改回新的 `credential_not_ready`（9 種語系文案），
不再是 `session_required`；AuthorizationGate 收到時顯示「請以密碼重新登入」，不重試、
不說「通常是暫時的」。App 自己包裝 `authorization.status` 時，要把此碼轉成 reason
`credential_not_ready`（例如 Reclaim 目前歸為 `service_unavailable`）。「請以密碼重新登入」文案改為同時適用
portal 頁：「無法取得 DSM 的驗證資訊。請以密碼重新登入 DSM，再從 DSM 桌面開啟 {package}。」

Behavior changes:

- `executor.status` (`delegate.ExecutorRoutes`, and the bridge runtime's
  route) refuses with `APP_STOPPED` while the lifecycle mailbox says the
  package is stopped, and with `APP_STARTING` in the 10 seconds after a
  start, as the `root.*` services do. It answered a paused status
  (`executor_not_listening`), which the CLI's root window read as
  `executor_status_mismatch` and answered with the approval form, without
  checking again once the package ran. A paused executor whose
  start is older still answers `paused`.
- `api.DSMSession` refuses a request whose single DSM session cookie came
  without a SynoToken with the new `credential_not_ready` (`api.CredentialNotReady`)
  instead of `session_required`. Every service that acts with the request's
  DSM session (`root.*`, `files.*`, `delegate.*`, File Station) answers it.
  It is a `service` code of `spec/bridge/v1/errors.json` with text in every
  UI locale, which an App may pass on. A malformed token, or no cookie,
  stays `session_required`. On an Application Portal page and in a DSM
  session restored without a password the page has no SynoToken, and Apps
  said "this is usually temporary". An App whose own
  `authorization.status` reports the executor's answer should map this code
  to reason `credential_not_ready`.
- `AuthorizationGate` words a check refused with `credential_not_ready` as
  the credential case (`authorization.credential.*`: sign in to DSM again
  with a password), without the retry note, and does not retry it in the
  background, as an expired session.
- `ExecutorSettings` shows, while its first status loads, the status row it
  will show (`[data-placeholder-row]`) instead of the section's three
  skeleton lines, as `PortalSettings` and `BackgroundSaveSettings` do since
  0.10.5. The three lines gave way to one row and moved Reclaim's settings
  below them (CLS: 136px with `PortalSettings`' rows).
- `DelegatePanel` and `BackgroundPanel` no longer pass `loading` to
  `SettingsSection` either: while their first answer loads, `DelegatePanel`
  shows its form (fields off) and its two lists as waiting rows, and
  `BackgroundPanel` its status lines as waiting rows and its two buttons off
  (`[data-placeholder-row]`), not three skeleton lines. No SDK section draws
  the section skeleton now; `AuthorizationSettings` already showed its
  status row ("Checking"). An App test that waited for
  `[data-ui-skeleton-line]` in these panels should wait for
  `[data-placeholder-row]`.
- `DelegatePanel` reads a refusal with `APP_STOPPED`, `APP_STARTING` or
  `EXECUTOR_STARTING` (which `delegate.status` answers since 0.10.5) as a
  stopped or starting package: it says so (`appStopped`, `appStarting`)
  instead of showing an error, and checks again on its own after 2 s,
  doubling to at most 15 s, as `ExecutorNotice`, `AuthorizationGate` and
  `BackgroundSaveSettings` do.
- The headless entry exports `appLifecycle(error)`, which reads a refusal
  or status naming a stopped or starting package, and `lifecycleCopy(lifecycle,
  locale)`, the panels' text for it (9 locales), with `lifecycleRetryDelay`
  and `LIFECYCLE_RETRY_MAX_MS`: an App's own polling hands these codes to the
  SDK instead of keeping a code table and text of its own.
- `delegate.Local(packageID)` reads the package's lifecycle mailbox
  (`/var/packages/<id>/var/executor`) by default, as `LocalRoot` did, and the
  bridge runtime's executor clients read `<data dir>/executor`: a stopped or
  starting package answers `APP_STOPPED` or `APP_STARTING` on every executor
  call (such as `delegate.status` and a Go App's own `delegate.Service`),
  where an App that did not set `Mailbox` got `EXECUTOR_UNAVAILABLE`.
A call in the 10 s after a start now waits for the executor to
  listen (`StartGrace`). A client built as `&delegate.Client{Socket: …}`
  still reads no mailbox.
- `authorization.credential.body` (9 locales) no longer says the DSM session
  was restored: it reads "DSM's sign-in information could not be obtained.
  Sign in to DSM again with your password, then open {package} from the DSM
  desktop.", which also fits an Application Portal page.

## 0.10.5 (2026-10-14)

Fixes from the 0.10.4 acceptance run. The executor source did not
change, so the executor and lifecycle digests equal 0.10.4: an App rebuilt on
0.10.5 keeps its approval unless its own root programs change. (Corrected
after release: Reclaim's root program does not use `syno-pkg-land-app`, so
its digest did not change and Reclaim 1561 kept its approval; see
0.10.6.) The
Vue entry is tested against UI 0.9.7; its peer range stays `^0.9.5`.

Incompatible: none in the API. Behavior that an App's own tests may see:
`delegate.status` refuses with `APP_STOPPED`, `APP_STARTING` or
`EXECUTOR_STARTING` while the package is stopped or starting instead of
answering `blocked`; the saves list is "Save history" (「存檔紀錄」, was
「我的存檔」) and pages five saves instead of ten; settings sections show
their final rows while loading, marked `[data-placeholder-row]`, instead of
`[data-ui-skeleton-line]` skeletons; `AuthorizationGate` words a refused
check (HTTP 401 or 403) as denied, not as temporary. A DSM window whose
`ctx.call` repeats these codes for 30 s (CLI 0.10.4) delays the panels'
own "starting" state; CLI 0.10.5 answers the status reads at once.

繁中摘要：executor 原始碼未變，executor／lifecycle digest 與 0.10.4 相同，App 用 0.10.5
重建不需重新核准；只有 App 自己的 root 程式位元組改變時才要重新核准（發版後更正：Reclaim
的 root 程式不含本 SDK，1561 不需重新核准，見 Unreleased）。App 自己的測試可能受影響：`delegate.status` 改回拒絕碼、「存檔紀錄」每頁
5 筆、載入佔位改 `[data-placeholder-row]`、Gate 403 文案。套件停止或啟動中時，ExecutorNotice、BackgroundSaveSettings、
AuthorizationGate 不再顯示「需要核准」，改顯示「App 已停止／啟動中」並自行重試
（2 s 起倍增，上限 15 s）；AuthorizationGate 依原因顯示文案（403、設定錯誤不再說
「通常是暫時的」）；設定頁區塊載入時先顯示最終的列、清單高度與頁尾（CLS）；核准表單
沿用視窗已讀到的狀態，不再讀第二次；「我的存檔」改名「存檔紀錄」並說明檔案之後可能
已被移動或刪除，每頁 5 筆；開啟按鈕在 File Station 中選取該檔案；ExecutorSettings
改用 SettingsRow；AppShell 的 notices 沒有實際內容時不佔位。

Behavior changes:

- `delegate.status` (Go `delegate.Service`) refuses with `APP_STOPPED`,
  `APP_STARTING` or `EXECUTOR_STARTING` while the package is stopped or
  starting, as `delegate.destination` does, instead of answering a blocked
  status that the browser read as "needs approval".
- `ExecutorNotice`, `BackgroundSaveSettings` and `AuthorizationGate` say the
  app is stopped or starting (new `delegate.appStopped`/`appStarting` and
  `authorization.stopped`/`starting`, 9 locales) and check again on their
  own after 2 s, doubling to at most 15 s, until the package answers. An
  administrator no longer sees the approval notice for a status that cannot
  be read.
- `AuthorizationGate` names why a check failed: a refused check (HTTP 401 or
  403, `admin_required`, `APP_DENIED`, …) and a misconfigured one no longer
  say "this is usually temporary" (`authorization.denied`/`config`, 9
  locales), and only a temporary failure adds "You can try again".
- `BackgroundSaveSettings` shows, while its first status loads, the scope and
  folder rows, the saves list's height and the administrator's footer; it
  reads the saves list together with the status. The saves list keeps a
  page's height whatever it holds and pages five saves instead of ten; the
  pager's room follows the count this tab last showed. `PortalSettings` shows
  its link, alias and actions rows in place of a three-line skeleton. In
  Chromium the content below moved 3 times (527px in all) and now moves 2px,
  or 36px for the pager on a tab's first open with more than five saves.
- `ExecutorApproval` passes the status it read to `ExecutorSettings`, whose
  first load no longer reads it again.
- The saves list is "Save history" (`save.operations`, new
  `save.operationsHelp`, 9 locales): it records how each save ended, and a
  file moved or deleted since keeps its "Saved" row.
- The saves list's File Station button selects the saved file
  (`openFileInFileStation`) instead of only opening its folder.
- `ExecutorSettings` lays its status, details and scope out in `SettingsRow`,
  as `AuthorizationSettings` and the other settings sections, so they stack
  alike at a narrow width.
- `AppShell` hides its notices block while the notices render nothing, such
  as a notice component whose own condition is false; it left a 16px gap
  under the page header.

## 0.10.4 (2026-10-14)

Retryable codes for a stopped or starting package, the bridge passing the
executor client's codes on, and fixes from the 0.10.3 acceptance run.
The executor source did not change, so the executor and lifecycle digests
equal 0.10.3: an App rebuilt on 0.10.4 keeps its approval unless its own
root programs change. (Corrected after release: a root program changes
only when its own bytes change; see Unreleased.) The Vue entry is tested against UI 0.9.6; its peer range stays
`^0.9.5`, since it uses nothing new in 0.9.6.

Incompatible: none in the API. Behavior that an App's own tests may see:
`BackgroundSavePanel` no longer renders a saves list of its own (the list is
in `BackgroundSaveSettings`, and the sample save moved to its `operations`
slot); a bridge App's `EXECUTOR_UNAVAILABLE`, `APP_STOPPED` and the other
client codes reach the browser with their code instead of `internal`.

繁中摘要：新增可重試碼 `APP_STOPPED`／`APP_STARTING`；bridge 轉送 executor client
碼；0.10.3 驗收修正（「我的存檔」清單、停用前確認、核准中按鈕、設定頁佔位、zh-CN
注销、非 admin 不顯示待核准）。executor 原始碼未變，executor／lifecycle digest 與
0.10.3 相同，App 用 0.10.4 重建不需重新核准；只有 App 自己的 root 程式位元組改變時才
要重新核准（發版後更正，見 Unreleased）。測試用 UI 0.9.6，
peer 仍為 `^0.9.5`。

Behavior changes:

- New retryable client codes `APP_STOPPED` (the package is stopped) and
  `APP_STARTING` (the package was just started and its executor does not
  listen yet). Neither sends anything to the executor.
- A bridge App (Rust, Python, Node) may pass on the executor client's codes
  (`EXECUTOR_UNAVAILABLE`, `APP_STOPPED`, …) as it passes on a refusal; the
  runtime used to report them as `internal`, so the browser showed an
  unknown error while the package was stopped or starting.
- The root client (`delegate.LocalRoot`, the bridge's `root.*` services)
  reads the package's lifecycle mailbox: a stopped package answers
  `APP_STOPPED`, and a call in the 10 seconds after a start waits for the
  executor to listen instead of failing (`delegate.Client.Mailbox`,
  `delegate.StartGrace`).
- `bridgeErrorCopy` translates `APP_STOPPED` and `APP_STARTING` in every UI
  locale (spec/bridge/v1/errors.json).
- `BackgroundSaveSettings` lists each user's own saves (queued, copying,
  failed, done) in a nested "My saves" section, so an App that lays out only
  the settings still shows them. `BackgroundSavePanel` no longer adds
  a list of its own; its sample save sits in the settings' new `operations`
  slot.
- `AppShell` passes `PageHeader`'s `meta` slot on as its own `meta` slot, and
  the Vue entry exports `PageActionsTarget`, so a page that uses
  `PageActions` can be mounted and tested without the shell.

Fixed:

- `ExecutorNotice` no longer tells a user who is not an administrator that
  the App waits for approval when the executor status cannot be read; it
  shows nothing.
- `BackgroundSaveSettings` asks before it turns background saves off, which
  revokes every folder and cancels pending saves (new save copy
  `disableConfirm`).
- `PortalSettings` keeps its place with a skeleton while its status loads,
  so the settings page no longer moves when it arrives.
- `BackgroundSaveSettings` shows the scope's change button at once, disabled
  until the scope is read, instead of adding it seconds later.
- The approval's confirm button (`ExecutorSettings`, `BackgroundSaveSettings`)
  shows its loading state while the approval runs.
- zh-CN background copy says 注销, as DSM does, instead of 退出登录; the
  glossary test refuses 退出登录, 帐号 and 帐户.

Docs:

- spec/protocol/v1 says what `EXECUTOR_UNAVAILABLE` means once `APP_STOPPED`
  and `APP_STARTING` are told apart: the package runs but its executor does
  not listen (not approved, awaiting approval after an upgrade, or failed).
  A browser client repeats `APP_STOPPED`, `APP_STARTING` and
  `EXECUTOR_STARTING` on its own and shows `EXECUTOR_UNAVAILABLE` as the
  approval state; the SDK's panels already do so.

## 0.10.3 (2026-10-14)

Approval: the executor changed, so its digest changes:
`scripts/executor-digests.sh 0.10.3` gives x86_64 `4ba70407…26ba`, aarch64
`ba80b389…b3d5` and armv7 `49474467…a299` (0.10.2: `bb7aebb1…3fae`,
`2fd8365a…bb89`, `6f757614…3979`). The lifecycle digests are those of
0.10.2. An App whose policy digest does not change puts the new executor
in place by itself when it upgrades and needs no approval (CLI 0.7.1 and
later); an App that declares the new start step (`root.start`, CLI 0.10.3)
changes its policy and waits for an administrator, as does a first install.

Behavior changes:

- The executor digest of every App changes (Approval, above).
- While a start step runs, the executor refuses every request but `status`
  with the new retryable code `EXECUTOR_STARTING`; `status` answers
  `ready: false` with `state: "starting"`. It is retryable, as
  `EXECUTOR_PAUSED` is: an App that calls the executor itself retries it.
  Without a start step the executor never answers it.
- The executor listens as soon as the package pins match; the approval
  tasks are read back after it listens.
- The Go client waits 40 seconds by default instead of 20, and the bridge's
  `root.start` and `root.jobs.*` 40 seconds instead of 140 (Changed, below).
- The executor creates its package staging folder when a large `root.run`
  output needs it, instead of answering `ROOT_FAILED`.
- New entry `@synopackageland/sdk/native` (`ROOT_RUNTIME`,
  `AUTHORIZATION_RUNTIME`), which the CLI's
  `@synopackageland/cli/authorization-sdk` used to carry.
- The Vue entry needs `@synopackageland/ui` 0.9.5.

Incompatible (details under Changed):

- `BackgroundSavePanel` loses its status badges, scope row and destination
  list, and the copy keys only they used are removed.
- `ExecutorSettings` no longer offers a scope change for an approved
  executor; `BackgroundSaveSettings` does.
- `@synopackageland/sdk/vue` needs `@synopackageland/ui` `^0.9.5`.

### Added

- `BackgroundSaveSettings` (`@synopackageland/sdk/vue`): background saves as
  one section of the App's settings page. A user sees the save folder and
  a button to choose another, with no password; an administrator also sees
  the allowed scope, with Change. A `Callout` shows only when something
  needs attention. `@destinations` reports each key's state after every
  read, so the App needs no `destination(key)` of its own.
- `AppShellPage.placement: 'bottom'`: the sidebar lists the page last,
  after every group, for an App's settings.

- `@synopackageland/sdk/native`: `ROOT_RUNTIME` and `AUTHORIZATION_RUNTIME`,
  the DSM window runtimes a native product with its own pinned root tasks
  renders into its window. They move unchanged from the CLI's
  `@synopackageland/cli/authorization-sdk`, which the CLI drops.

- `PageActions` (`@synopackageland/sdk/vue`): a page that `AppShell` renders
  as a `component` puts its own header buttons, such as its refresh, in the
  page header, before the App's `#actions`, and they leave with the page.
  Apps no longer teleport into a target of their own.
- A policy may name a root entry as its start step, `"start": "<name>"`.
  The executor runs it as root once after each install or upgrade, at the
  first start of a package version, with
  `install`, or `upgrade` and the previous version, for at most 60 seconds
  and only while the approval is live. While it runs, `status` answers
  `ready: false` with `state: "starting"` and every other request is refused
  with the new retryable code `EXECUTOR_STARTING`, so no session reaches the
  App's root work first. A failure is logged and never blocks
  the start; `status` reports the result as `start_step`. The entry cannot
  declare `run` or `job`. Every start now records the package version in the
  executor's protected state, so the executor digest changes; a policy
  without the step keeps its digest.

- `spec/scheduler/v1`: how an approval task is read back from DSM and
  judged, the DSM behavior that rests on (measured on DSM 7.4), and
  `readback.json`, the vectors the executor and the CLI's window approval
  both run in their tests, so their rules cannot drift apart.

### Changed

- The Vue entry needs `@synopackageland/ui` 0.9.5 (peer `^0.9.5`, was
  `^0.9.4`) and is tested against it: the settings panels use its shared
  label column, and `BackgroundSaveSettings` its button and callout styles.
  An App moves its `@synopackageland/ui` to 0.9.5 with the SDK.
- **Incompatible:** `BackgroundSavePanel` is `BackgroundSaveSettings` plus
  the user's saves. Its status badges, scope row and destination list are
  gone, and so are the copy keys only they used (`save.empty`,
  `untilRevoked`, `statusEnabled`, `statusDisabled`, `statusNeedsScope`,
  `delegations`, `scope`, `scopeNeeds`, `scopeMismatch`, `enabled`,
  `revoked`, `loading`; `delegate.scopeChangeHelp`, `confirmScope`,
  `hintUnchanged`). An App that merges its background and settings pages
  places `BackgroundSaveSettings` on its settings page instead (README,
  "Background saves in the settings page").
- **Incompatible:** `ExecutorSettings` no longer offers a scope change once
  the executor is approved with its scope, so an approved executor asks for
  no password; the scope changes in `BackgroundSaveSettings`. The DSM
  password is still asked for a scope change: the App backend sees the
  administrator's session on every request and could replay it, and the
  password is the one proof it never sees.
- A scope change whose root task lands after the DSM window stops waiting
  is read back from `delegate.status` for 60 seconds before the approval
  reports it failed, in `ExecutorSettings` and `BackgroundSaveSettings`.

  繁中摘要：背景存檔與設定合成一個區塊 `BackgroundSaveSettings`：使用者只看到「存放資料夾」與「選擇資料夾」，
  不需密碼；管理員多一列「允許的範圍」。首次核准同時選範圍，只輸入一次密碼；之後只有變更範圍才需要密碼
  （App 後端每次請求都拿得到管理員的 DSM session，密碼是它唯一看不到的證明）。`AppShellPage` 新增
  `placement: 'bottom'`，把設定頁放在側欄最底。`BackgroundSavePanel` 的狀態徽章、範圍列與授權清單移除。

- `ROOT_RUNTIME` (`@synopackageland/sdk/native`) reads Task Scheduler as
  `spec/scheduler/v1` says and runs its readback vectors: a step lists once
  and gets the task by that id; `state`, a `run` and the readback after a
  run get the id an earlier list showed, and list again when DSM answers
  4801 or another task. `authorize` and `remove` still list before they
  delete or create. A task is judged by `get` alone: a duplicate name and
  any difference from the exact definition and script stay not ready.

  繁中摘要：native root runtime 依 spec/scheduler/v1 讀 Task Scheduler 並跑共用向量：
  每步驟 list 一次，之後用快取 id get，4801 或 id／名稱不符就重新 list；
  會刪除或建立的步驟仍先 list。重複名稱與逐字比對照舊。

- Executor timeouts have one source, `spec/protocol/v1` Timeouts and its
  `timeouts.json`: every layer waits longer than the layer it calls. The Go
  client (`delegate.Local`, a `delegate.Client` without `Timeout`) waits 40
  seconds (`delegate.ExecutorTimeout`), past the executor's 30, instead of
  20; `root.run` still waits 140. The bridge runtime's `root.start` and
  `root.jobs.*` wait 40 seconds instead of 140; `bridge.RootTimeout` is
  `delegate.RootRunTimeout`. An App whose backend awaits `root.run` needs a
  request deadline of at least the program's `run.timeout_s` + 20 seconds.

  繁中摘要：executor 呼叫鏈的逾時改由 spec/protocol/v1 Timeouts 與 timeouts.json 統一定義，
  每層都比內層多等。Go client 預設等 40 秒（原 20）；bridge 的 root job 操作等 40 秒（原 140）。
  後端會等 root.run 的 App，request 逾時至少要是程式 timeout_s + 20 秒。

- A root step's approval check gets the control task by the id the
  executor's startup readback just listed, instead of listing Task Scheduler
  again; an id that is gone or names another task is listed again. Purge still
  lists. This removes one Task Scheduler list (0.5-15 s measured, slower
  under concurrent scheduler calls) before a due start step. The executor
  digest changes.

  繁中摘要：root 步驟的核准檢查改用 executor 啟動回讀時 list 到的 id 直接 get control 任務，
  不再重新 list；id 不見或指向別的任務就重新 list。purge 仍然 list。executor digest 改變。
- The settings panels follow the UI settings-page layout. `BackgroundSavePanel`
  and `ExecutorSettings` drop their own `10rem` label column and share the
  page's `--ui-label-width` from UI 0.9.5, so their rows line up
  with the App's `SettingsRow` and `FormItem` rows. The sub-groups of
  `BackgroundSavePanel`, `DelegatePanel` and `BackgroundPanel` are nested
  `SettingsSection`s instead of hand-drawn `<h3>` headings; the background
  schedules heading no longer reads larger than its section title.
  `BackgroundSavePanel` takes `title`: an App whose page header already says
  "Background saving" passes `title=""` to drop the repeated heading.
- The executor opens its socket as soon as the package pins match. Reading
  the two approval tasks back from DSM for the cleanup inventory, recording
  the package version and a due start step now follow while it listens;
  before, they ran first. The two tasks are read back at once. The pins
  check still precedes the socket, and the approval check still precedes
  every root step. Measured on a DSM 7.4 x86_64 NAS with a fresh install
  and the CLI's DSM window approval: without a start step the executor
  listened about 1 s after the boot task ran instead of 6.3 s, and the
  approval reached ready in 14.9 s instead of 17.0 s (DSM's own scheduler
  calls vary by several seconds between runs); with a 5 s start step it
  reached ready in 25.4 s instead of 33.2 s.
- The executor digests change (`scripts/executor-digests.sh`): executor
  x86_64 `4ba7040761a719e74d6b047ef68b9415e658ebb8b373b69555b8c307524e26ba`,
  aarch64 `ba80b389e3291419389b66be7b0a0e1085d3dfec55c9c651fecae427279bb3d5`,
  armv7 `49474467b9c9a7f31b30b5053b70eb4f71a95f2c06fbf3566e8de8f16972a299`;
  lifecycle x86_64
  `d65500c4f04713a32b45d360059281391acfa5e00ee24895fdd2de7b82735372`,
  aarch64 `94f6cd119186ff482b8aea56541dd1f9d53c147566bc1efdca6f5a1c86198331`,
  armv7 `cafc97c89f5645c14a8cf31d6d9f258394ed6f2d086478a7aebd04d57c274b4e`
  (go1.26.9). An App whose policy digest does not change needs no new
  approval (Approval, above).

  繁中摘要：executor 在 pins 驗證通過後就開始 listen，任務回讀、版本紀錄與啟動步驟移到 listen 之後；
  啟動步驟執行中只回應 status（`state: "starting"`），其他請求回 `EXECUTOR_STARTING`。
  executor digest 改變；policy digest 不變的 App 升級時自動換上，不需重新核准。

### Fixed

- `openFolderInFileStation` and `openFileInFileStation` launch only one
  canonical shared-folder path: a relative path, a volume root, `.`, `..`,
  an empty segment, a backslash or a control character returns false.
  `fileStationPath` strips only `/volumeN`, `/volumeUSBN` and
  `/volumeSATAN`, so a shared folder named like `volumes2` stays as it is.

  繁中摘要：File Station 只開啟單一標準共用資料夾路徑；相對路徑、volume 根目錄、`.`、`..`、
  空段、反斜線或控制字元一律不開啟。只去掉 DSM volume 掛載前綴。
- A delegate move the executor refuses with `EXECUTOR_STARTING` releases
  its admission and stays recoverable, as with `EXECUTOR_PAUSED`, instead of
  failing the job.
- `ExecutorSettings` explains the CLI window's `executor_starting` result,
  an executor whose start step outlasted the approval's wait, in every SDK
  language instead of showing only the English reason.
- `ExecutorSettings`, `ExecutorApproval` and `ExecutorNotice` read the
  `code` the DSM window's access now reports with a failed approval (CLI
  `root-window`), so the App shows its own language instead of the English
  reason. A refused, missing or unconfirmed administrator password
  (`password_rejected`, `password_required`, `password_confirm_failed`) keeps
  the approval form with its own text in every SDK language; `session_ended`
  asks to sign in to DSM again. An older CLI's `dsm_3121` reason still reads
  as a refused password. `CapabilityStatus` declares `code` and `outcome`.
- Simplified Chinese: the sign-in page writes account as 账户, not 帐户,
  and the background jobs read 系统繁忙 for busy and 运行 for a scheduled
  run, as DSM does in zh-CN. `ts/test/glossary.test.ts` refuses 帐户 and
  忙碌 outside DSM glossary strings.

  繁中摘要：簡中登入頁「帐户」改為「账户」，背景工作改用 DSM 簡中用語。

- The executor creates the package staging folder
  (`/var/packages/<package>/var/executor-staging`, owned by the package
  account, mode 0700) when a `root.run` output passes 64 KiB and the folder
  is missing. Before, every such run answered `ROOT_FAILED` and logged
  nothing. A staging folder the executor still cannot use is `ROOT_FAILED`
  with the message `root output staging unavailable`, and executor.log
  names the program and the cause. The executor digest changes (Approval,
  above).
- The spec states that a `root.run` result's `staging` is a bare file name
  (`root-<32 hex>`), never a path: the App joins it to its
  `executor-staging` folder (`$SYNOPKGLAND_DATA_DIR/executor-staging` behind
  the bridge). Go's `delegate.Root` already does.
- The executor logs each `ROOT_BUSY` refusal of `root.run` with the program
  name. A client that stops waiting does not stop its program, which keeps
  one of the two slots until it ends.
- `executor check-policy` and the executor refuse a policy whose `stop` or
  `purge` entry declares `run` or `job`, naming the entry. The executor
  always denied sessions that entry, so such a policy passed the check and
  then answered every `root.run` and `root.start` to it with
  `POLICY_DENIED`. Give the session work its own entry; it may pin the same
  digest. A shorthand-digest step stays valid.
- `executor check-policy` and the executor refuse a policy with a `files.*`
  operation but no `delegate.create`: every files op acts through a
  delegation only `delegate.create` makes, so each call would be denied.
- `ExecutorApproval`, `ExecutorSettings` and `ExecutorNotice` keep a failed
  approval on screen until the administrator submits again, refreshes or
  closes the dialog; a status read no longer clears it. A password DSM
  refuses (`dsm_3121`) reads "The DSM password is incorrect" in every SDK
  language, instead of DSM's English reason, "Unavailable" and a hint to
  check the App setup.
- `ExecutorSettings` (and so `ExecutorApproval` and `ExecutorNotice`)
  explains a failed executor status request (`<code>: the executor status
  request failed.`) in every SDK language with its likely causes: an
  Application Portal alias with an App built by an older CLI, an expired
  DSM sign-in, or a service that has not started or has stopped. The code
  stays below it as detail. The portal cause itself is fixed in the CLI's
  generated window; rebuild the App with that CLI.

## 0.10.2 (2026-10-14)

First release of the changelog. The SDK is the App-side code of a
synopkgland package:

- Go module `github.com/synopackageland/syno-pkg-land-sdk`: the API server
  with DSM session and permission checks (`api`), background jobs, data
  migration, delegated operations, DSM and File Station access, identity,
  JSON limits and the Application Portal.
- `executor/`: the delegated executor that runs an App's approved root and
  delegate operations, standard library only. `executor check-policy`
  validates a policy before approval. `scripts/executor-digests.sh 0.10.2`
  gives the digests the CLI ships.
- `spec/protocol/v1/`: the executor protocol, its op schemas, `errors.json`
  and test vectors.
- `bridge/cmd/runtime` and `spec/bridge/v1/`: the package CGI that hands
  verified calls to a Python, Node.js or Rust App, one process per request.
- App SDKs for the bridge: `syno-pkg-land-app` (Rust), `@synopackageland/app`
  (Node.js 18 or later) and `synopkgland_app` (Python 3.8 or later).
- npm package `@synopackageland/sdk`: the headless client and Vue components
  (`@synopackageland/sdk/vue`).
