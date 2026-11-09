import type {HostRequestResult} from '../context'

/** A verified DSM application ID, independent of the package ID. No credentials. */
export interface DsmAppTarget {
 appId: string
 params?: Readonly<Record<string, string>>
}
/** requested acknowledges dispatch only; it says nothing about opening or completion. */
export type DsmAppLaunchResult = HostRequestResult
export interface DsmAppLauncher {
 openApp(target: DsmAppTarget): Promise<DsmAppLaunchResult>
}

/** The same SYNO.SDS desktop contract used by File Station navigation. */
export interface DsmDesktop {
 AppLaunch?:(id:string,params?:Readonly<Record<string,string>>)=>unknown
 StatusNotifier?:{isAppEnabled?:(id:string)=>boolean;isAppLaunchable?:(id:string)=>boolean}
}
export interface DsmAppLauncherOptions {
 /** Defaults to this page's SYNO.SDS only. Never reads parent or top globals. */
 desktop?: DsmDesktop
}

export const MAX_DSM_APP_PARAMS = 32
export const MAX_DSM_APP_PARAM_LENGTH = 2048
/** Maximum encoded launchParam length, before the outer query-string encoding. */
export const MAX_DSM_APP_LAUNCH_PARAM = 8192
const controls = /[\u0000-\u001f\u007f-\u009f]/
const appId = /^[A-Za-z][A-Za-z0-9_.-]{0,255}$/
const paramKey = /^[A-Za-z][A-Za-z0-9_.-]{0,63}$/
// AppLaunch interprets these as desktop configuration, or calls them itself.
const desktopKeys = new Set(['className','instanceName','dsm_version','title','hasOwnProperty'])
const secretKey = /^(?:password|passwd|pwd|token|synotoken|accesstoken|refreshtoken|idtoken|authorization|secret|clientsecret|apikey|credential|credentials|sid|sessionid)$/i

/** Shared validation and DSM's existing double-encoding contract. Throws without echoing input. */
export function dsmAppTarget(target: DsmAppTarget): {appId:string;params:Record<string,string>;encoded:string} {
 if(!target || typeof target.appId !== 'string' || !appId.test(target.appId)) throw new Error('Invalid DSM application ID')
 const raw = target.params
 if(raw !== undefined && (!raw || typeof raw !== 'object' || Array.isArray(raw))) throw new Error('Invalid DSM launch parameters')
 if(raw !== undefined && Object.getPrototypeOf(raw) !== Object.prototype && Object.getPrototypeOf(raw) !== null) throw new Error('Invalid DSM launch parameters')
 const entries = Object.entries(raw ?? {})
 if(entries.length > MAX_DSM_APP_PARAMS) throw new Error('Too many DSM launch parameters')
 const params:Record<string,string> = Object.create(null)
 const encoded = entries.map(([key,value]) => {
  if(!paramKey.test(key) || desktopKeys.has(key) || secretKey.test(key.replace(/[_.-]/g,'')) ||
   typeof value !== 'string' || value.length > MAX_DSM_APP_PARAM_LENGTH || controls.test(value)) throw new Error('Invalid DSM launch parameter')
  params[key] = value
  // Also rejects malformed Unicode, before anything reaches the host.
  return `${encodeURIComponent(key)}=${encodeURIComponent(value)}`
 }).join('&')
 if(encoded.length > MAX_DSM_APP_LAUNCH_PARAM) throw new Error('DSM launch parameters are too long')
 return {appId:target.appId,params,encoded}
}

/** Synchronous dispatch shared with existing File Station navigation. */
export function requestDsmApp(target:DsmAppTarget,options:DsmAppLauncherOptions={}):DsmAppLaunchResult {
 let checked:ReturnType<typeof dsmAppTarget>
 try {checked=dsmAppTarget(target)} catch {return {status:'invalid-input'}}
 return dispatchDsmApp(checked,options)
}

// File Station has its own established path bounds and validates before dispatch.
export function dispatchDsmApp(checked:DsmAppTarget,options:DsmAppLauncherOptions):DsmAppLaunchResult {
 try {
  const desktop = 'desktop' in options ? options.desktop : (globalThis as {SYNO?:{SDS?:DsmDesktop}}).SYNO?.SDS
  if(typeof desktop?.AppLaunch !== 'function') return {status:'unsupported'}
  const status=desktop.StatusNotifier
  if(status?.isAppEnabled?.(checked.appId)===false || status?.isAppLaunchable?.(checked.appId)===false) return {status:'unavailable'}
  // DSM calls config.hasOwnProperty after its async script/task-button load.
  // Keep validation's prototype-free record internal; DSM needs a mutable Object.
  desktop.AppLaunch(checked.appId,{...checked.params})
  return {status:'requested'}
 } catch {return {status:'failed'}}
}

/** Launch in the current DSM desktop, or unsupported outside it. No navigation fallback. */
export async function openApp(target:DsmAppTarget,options:DsmAppLauncherOptions={}):Promise<DsmAppLaunchResult> {
 return requestDsmApp(target,options)
}

/** Injectable platform capability; legacy HostAdapter.openApp(string) remains available. */
export function createDsmAppLauncher(options:DsmAppLauncherOptions={}):DsmAppLauncher {
 return {openApp:target=>openApp(target,options)}
}
