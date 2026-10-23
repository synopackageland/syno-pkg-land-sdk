export interface HostPlatformSnapshot {
 readonly web: boolean
 readonly host: 'dsm' | 'standalone-web' | 'unavailable'
 readonly dsmStandalone: boolean | null
 readonly dsmLanguage: string | null
 readonly appLocale: string | null
 readonly systemVersion: string | null
 readonly fullVersion: string | null
 readonly theme: null
}
export type HostTitleResult = {status: 'ok'; target: 'dsm-window' | 'document'} |
 {status: 'unsupported' | 'unavailable' | 'invalid-input' | 'failed'}
export type HostRequestResult = {status: 'requested'} |
 {status: 'unsupported' | 'unavailable' | 'invalid-input' | 'blocked' | 'failed'}
export type HostURLResult = {status: 'requested'; windowHandleAvailable: boolean} |
 {status: 'unsupported' | 'unavailable' | 'invalid-input' | 'failed'}
export type HostEvent = 'window-resize' | 'host-theme-change' | 'host-language-change'
export type HostSubscriptionResult = {status: 'subscribed'; dispose(): void} |
 {status: 'unsupported' | 'unavailable' | 'invalid-input' | 'failed'}
export type HostFileNavigationResult = {status: 'requested'; action: 'locate-folder' | 'highlight-file'} |
 {status: 'unsupported' | 'unavailable' | 'invalid-input' | 'failed'}
export type HostUnsupportedResult = {status: 'unsupported' | 'unavailable'}
export interface HostAdapter {
 platform(): HostPlatformSnapshot
 setTitle(title: string): HostTitleResult
 activateWindow(): HostRequestResult
 requestClose(): HostRequestResult
 openApp(appId: string): HostRequestResult
 openURL(url: string): HostURLResult
 openFileManager(path: string): HostFileNavigationResult
 revealFile(path: string): HostFileNavigationResult
 openFile(path: string): HostUnsupportedResult
 showFileDetails(paths: readonly string[]): HostUnsupportedResult
 openAppSetting(): HostUnsupportedResult
 subscribe(event: HostEvent, listener: () => void): HostSubscriptionResult
 dispose(): void
}
export interface PortalStatus {
 state: 'active' | 'inactive' | 'unavailable'
 // Set when active: https://<the NAS address in use>/<alias>/
 alias?: string
 url?: string
 // An administrator can set or clear the alias here; otherwise Control Panel or nothing.
 canManage: boolean
 controlPanel: boolean
 defaultAlias: string
 // Set when the App's executor registers the entry (below DSM 7.2): DSM lists
 // the App only while registered; cleanup-required asks for unregister again.
 entry?: 'registered' | 'unregistered' | 'cleanup-required'
}

// Rejects {code}: portal_alias_invalid, portal_alias_reserved, portal_alias_in_use, admin_required, portal_unavailable.
export interface PortalAccess {
 // SDK panel text in DSM's language, keyed like the error codes plus title, link, apply, …
 messages: Record<string, string>
 status(): Promise<PortalStatus>
 // The status read last, if any; PortalSettings starts from it. The DSM window's
 // access reads the status when the window opens; older CLIs leave it out.
 lastStatus?(): PortalStatus | undefined
 apply(alias: string): Promise<PortalStatus>
 disable(): Promise<PortalStatus>
 // Present with entry: an administrator has the executor add or remove the entry.
 register?(): Promise<PortalStatus>
 unregister?(): Promise<PortalStatus>
 openControlPanel(): void
 // The SDK's plain panel, on the DSM window's access only; Vue apps use
 // portal/PortalSettings.vue, and createPortalAccess leaves it out.
 mountPanel?(element: HTMLElement): {refresh(): Promise<void>; dispose(): void}
}

// Shared Folders the install wizard stored (Go sharedfolder.Config.Status): what
// the package account may do in each, against what the App declared it needs.
export type SharedFolderLevel = 'none' | 'read' | 'write'
export interface SharedFolderItem {
 key: string
 name: string
 path?: string
 exists: boolean
 required: SharedFolderLevel
 granted: SharedFolderLevel
 // pending: an administrator must grant the account more access; missing and
 // ambiguous are fixed by choosing the folder again, not by a permission.
 state: 'ready' | 'pending' | 'missing' | 'ambiguous'
 error?: string
}
export interface SharedFolderStatus {
 // The package account, as DSM lists it under System internal user.
 account: string
 folders: SharedFolderItem[]
 pending: boolean
}

// Rejects {code}: shared_folder_unavailable, admin_required, shared_folder_custom_acl,
// shared_folder_grant_failed, shared_folder_grant_unverified.
export interface SharedFolderAccess {
 // SDK panel text in DSM's language, keyed like the error codes plus title, steps, grant, …
 messages: Record<string, string>
 status(): Promise<SharedFolderStatus>
 lastStatus?(): SharedFolderStatus | undefined
 // True for an administrator in a window that offers DSM's session channel;
 // otherwise the panel shows the Control Panel steps only.
 canGrant(): boolean
 // Gives the package account the access the App declared, in the
 // administrator's DSM session, and resolves with the status the backend then measures.
 grant(key: string): Promise<SharedFolderStatus>
 openControlPanel(): void
}

// The native authorization runtime (root tasks of a native product); AuthorizationGate renders it.
export interface AuthorizationStatus {
 command: 'authorization'
 schema_version: 1
 state: 'ready' | 'authorization_required' | 'not_admin' | 'session_expired' | 'unavailable'
 reason?: string | null
}
export interface AuthorizationAccess {
 canManage(): boolean
 status(): Promise<AuthorizationStatus>
 request(): Promise<AuthorizationStatus>
 revoke(): Promise<AuthorizationStatus>
}

// The App backend's request deadline in milliseconds (Go api.Timeout); 60
// seconds (Go api.DefaultRequestTimeout) unless the App sets its own.
export const DEFAULT_REQUEST_TIMEOUT=60000
// A client waits this long for a reply: past the backend deadline, so the
// backend's own request_timeout reply arrives first.
export function clientTimeout(requestTimeout=DEFAULT_REQUEST_TIMEOUT):number{return requestTimeout+2000}

// A call's options. An abort of signal rejects the call with {code:
// 'request_canceled'}; past clientTimeout(requestTimeout) it rejects with
// {code: 'request_timeout'}.
export interface CallOptions {signal?: AbortSignal}

// String values under keys of letters, digits, _ . and -; the CLI's DSM
// window leaves out DSM's own keys, long values and control characters.
export type LaunchParams = Readonly<Record<string, string>>

export interface Context {
 // The App backend's request deadline in milliseconds; set it with the
 // backend's own. SDK clients wait clientTimeout(requestTimeout).
 requestTimeout?: number
 authorization?: AuthorizationAccess
 // Explicit long-running opt-in; bind with createBackgroundSaveFacade and an
 // owner-bound App job metadata provider. Time-limited downloads use DelegatePanel.
 delegate?: import('./delegate/long-running').BackgroundSaveFacade
 // endpoint is legacy App CGI metadata and ignored by File Station. Browser
 // file I/O always goes to DSM on the current origin; token remains reactive.
 files?: {endpoint?:string;token?:()=>string|undefined}
 // The package's display name and icon URL from the DSM window; a full-page
 // shell names the App with them where a DSM window has a title bar.
 displayName?: string
 appIcon?: string
 // A Portal or standalone launch owns the whole page; read it with isFullPage.
 fullPage?: boolean
 executor?: import('./delegate/executor-setup').ExecutorSetup
 // DSM's signed-in account. isAdmin only hides or disables administrator
 // settings; the backend enforces access with permissions.json and RequireAdmin.
 user?: {readonly name: string; readonly isAdmin: boolean}
 // DSM Application Portal, present only when dsm-package.yaml declares portal.
 portal?: PortalAccess
 // DSM's WebAPI in the signed-in user's own session (a DSM error rejects {code:
 // 'dsm_<code>'}); the window provides it where it can. Optional: an App that
 // reads it (SharedFolderSettings) offers the guided steps without it.
 webApi?: (api: string, method: string, version: number, params: Record<string, unknown>) => Promise<unknown>
 batch?: import('./client').Batch
 call<T = unknown>(method: string, params?: Record<string, unknown>, options?: CallOptions): Promise<T>
 reopen(fn: () => void): () => void
 // The App's own values from the launch that opened the DSM window: a desktop
 // URL's launchParam or SYNO.SDS.AppLaunch's param. Untrusted text; read the
 // App route with launchRoute, or follow it with followLaunchRoute.
 launchParams?: LaunchParams
 // Runs fn with the values of each later launch of the open window that has any.
 onLaunch?(fn: (params: LaunchParams) => void): () => void
 locale: string
 // DSM's language code (enu, cht, …) that picked locale.
 lang?: string
 // Translates with the App's own catalogs (ui i18n) in locale: missing keys
 // fall back to English, then the key; {name} takes params.name, and
 // params.count picks key.one or key.other.
 t?: (key: string, params?: Record<string, unknown>) => string
 // DSM's AppWindow behind host; opaque to the App, which uses host instead.
 appWindow?: unknown
 host: HostAdapter
}
/** What tells a full page apart: the launch, or the host when an older CLI omits it. */
export type LaunchContext = {fullPage: boolean; host?: Pick<HostAdapter, 'platform'>} | {fullPage?: boolean; host: Pick<HostAdapter, 'platform'>}
/** What AppShell reads: the launch, the brand, the signed-in account, the executor to approve and, with app-root, the window to bring to the front. */
export type ShellContext = LaunchContext & Pick<Context, 'displayName' | 'appIcon' | 'user'> &
 Partial<Pick<Context, 'executor' | 'call' | 'reopen'>> & {host?: Partial<Pick<HostAdapter, 'activateWindow'>>}
// Older CLIs pass no fullPage: then only a DSM desktop window is not a full page.
export function isFullPage(ctx: LaunchContext): boolean {
 if(ctx.fullPage !== undefined) return ctx.fullPage
 if(!ctx.host) throw new Error('isFullPage: the context has neither fullPage nor host; pass fullPage (true for a page of its own), or a host (createStandaloneHost() outside DSM)')
 const platform = ctx.host.platform()
 return platform.host !== 'dsm' || platform.dsmStandalone === true
}
export interface AppStatus {package: string; version: string; user: string; admin: boolean}
