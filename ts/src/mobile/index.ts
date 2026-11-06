/** User-mediated mobile actions, with browser fallbacks. Importing creates no listeners. */
import type {HostAdapter, HostPlatformSnapshot, LaunchParams} from '../context.js'
import {createStandaloneHost} from '../standalone.js'
import {launchRoute} from '../launch.js'
import {
 NATIVE_PROTOCOL, NATIVE_EVENT_NAME, createNativeClient, isNativeCapabilities, isNativeShell, isNativeEvent,
 type NativeBridge, type NativeIntakeItem, type NativeTransfer,
} from './contract.gen.js'

export type {NativeBridge, NativeIntakeItem, NativeTransfer} from './contract.gen.js'
export interface MobileAction {readonly mode: 'native' | 'browser'; readonly id?: string}
export interface SaveOptions {readonly filename: string; readonly mime?: string}

/** Detection is deliberately main-frame only. The shell owns the NAS-origin injection boundary. */
export function nativeBridge(): NativeBridge | undefined {
 if(typeof window === 'undefined') return undefined
 try {
  if(window.top !== window || !/^https?:$/.test(window.location.protocol)) return undefined
  const bridge = (window as Window & {SynoPkgNative?: NativeBridge}).SynoPkgNative
  if(bridge?.protocol === NATIVE_PROTOCOL && typeof bridge.post === 'function' && isNativeShell(bridge.shell) && isNativeCapabilities(bridge.caps)) return bridge
 } catch { /* An inaccessible frame is a browser host. */ }
 return undefined
}

/** True only when the native URL-download capability is available. */
export function canSaveNativeURL(): boolean {
 const download = nativeBridge()?.caps.download
 return !!download && download.url
}

function httpURL(input: string): URL {
 let url: URL
 try {url = new URL(input, typeof location === 'undefined' ? undefined : location.href)} catch {throw {code: 'invalid_url'}}
 if(!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw {code: 'invalid_url'}
 return url
}
// Bound native basenames by UTF-8 bytes too, without cutting a Unicode character.
function filenamePrefix(value: string, limit: number): string {
 let result = '', size = 0
 const encoder = new TextEncoder()
 for(const char of value) {
  const bytes = encoder.encode(char).length
  if(size + bytes > limit) break
  result += char; size += bytes
 }
 return result
}
function saveFilename(input: string): string {
 const name = input.replace(/[/\\\u0000-\u001f\u007f-\u009f]/g, ' ')
  .replace(/\s+/gu, ' ').replace(/\s*\.\s*/g, '.').replace(/\.+/g, '.')
  .replace(/^[.\s]+|[.\s]+$/g, '') || 'download'
 const dot = name.lastIndexOf('.')
 const extension = dot > 0 ? name.slice(dot) : ''
 const stemByteLimit = 255 - new TextEncoder().encode(extension).length
 // Keep the final extension when it leaves room for at least one stem character.
 const stem = filenamePrefix(name.slice(0, dot > 0 ? dot : name.length), stemByteLimit)
 return (stem ? stem.replace(/[.\s]+$/g, '') + extension : filenamePrefix(name, 255)).replace(/[.\s]+$/g, '') || 'download'
}
function saveOptions(options: SaveOptions): {filename: string; mime: string} {
 if((options.mime?.length ?? 0) > 255 || /[\r\n]/.test(options.mime ?? '')) throw {code: 'invalid_filename'}
 return {filename: saveFilename(options.filename), mime: options.mime ?? 'application/octet-stream'}
}
let serial = 0
function requestId(): string {return `download-${Date.now().toString(36)}-${++serial}`}
function downloadLink(url: string, filename: string): MobileAction {
 if(typeof document === 'undefined') throw {code: 'unavailable'}
 const a = document.createElement('a')
 a.href = url; a.download = filename; a.rel = 'noopener'
 document.body.append(a)
 try {a.click()} finally {a.remove()}
 return {mode: 'browser'}
}
function blobBase64(blob: Blob): Promise<string> {
 return new Promise((resolve, reject) => {
  const reader = new FileReader()
  reader.onerror = () => reject({code: 'blob_read_failed'})
  reader.onload = () => resolve(String(reader.result).split(',')[1])
  reader.readAsDataURL(blob)
 })
}

/** Resolves when handed to the shell/browser, not when saved. Large blobs use a browser download. */
export async function saveBlob(blob: Blob, filename: string): Promise<MobileAction> {
 const options = saveOptions({filename, mime: blob.type || 'application/octet-stream'})
 const bridge = nativeBridge(), cap = bridge?.caps.download
 if(bridge && cap && cap.base64 && blob.size <= cap.maxBase64) {
  const base64 = await blobBase64(blob), id = requestId()
  try {createNativeClient(bridge).post({v: 1, type: 'download', id, ...options, base64}); return {mode: 'native', id}} catch { /* Browser fallback on a synchronous dispatch failure. */ }
 }
 const url = URL.createObjectURL(blob)
 try {return downloadLink(url, options.filename)} finally {
  // Keep the object URL alive until the browser has consumed the click; a
  // fixed revocation delay races slow downloads. Release on page departure.
  window.addEventListener('pagehide', () => URL.revokeObjectURL(url), {once: true})
 }
}

/** A native transfer accepts only this NAS origin; other HTTP(S) URLs use a browser link. */
export async function saveUrl(input: string, options: SaveOptions): Promise<MobileAction> {
 const url = httpURL(input), fields = saveOptions(options), bridge = nativeBridge(), cap = bridge?.caps.download
 if(bridge && cap && cap.url && url.origin === location.origin) {
  const id = requestId()
  try {createNativeClient(bridge).post({v: 1, type: 'download', id, ...fields, url: url.href}); return {mode: 'native', id}} catch { /* Browser fallback. */ }
 }
 return downloadLink(url.href, fields.filename)
}

/** Call synchronously from a user gesture. The shell independently enforces activation and 1/s. */
export function openExternal(input: string): MobileAction {
 const url = httpURL(input)
 if(typeof window === 'undefined') throw {code: 'unavailable'}
 if(navigator.userActivation && !navigator.userActivation.isActive) throw {code: 'user_activation_required'}
 const bridge = nativeBridge()
 if(bridge?.caps.openExternal) {
  try {createNativeClient(bridge).post({v: 1, type: 'openExternal', url: url.href}); return {mode: 'native'}} catch { /* Browser fallback. */ }
 }
 window.open(url.href, '_blank', 'noopener')
 return {mode: 'browser'}
}

/** Route external and _blank HTTP(S) anchor taps. Without a cap the browser keeps its own routing. */
export function routeExternalLinks(root: Document | HTMLElement = document): () => void {
 const listener = (event: Event) => {
  const click = event as MouseEvent
  if(click.defaultPrevented || click.button !== 0 || click.metaKey || click.ctrlKey || click.shiftKey || click.altKey || !nativeBridge()?.caps.openExternal) return
  const anchor = click.composedPath().find(node => node instanceof HTMLAnchorElement) as HTMLAnchorElement | undefined
  if(!anchor || anchor.hasAttribute('download')) return
  let url: URL
  try {url = httpURL(anchor.href)} catch {return}
  if(url.origin === location.origin && anchor.target !== '_blank') return
  // Dispatch while user activation is still live. A synchronous native failure
  // may already have opened a browser tab, so consume this tap in either mode.
  try {openExternal(url.href); click.preventDefault()} catch { /* Retain browser routing. */ }
 }
 root.addEventListener('click', listener)
 return () => root.removeEventListener('click', listener)
}

const taken = new WeakMap<NativeBridge, Set<string>>()
/** The id is an opaque read-once handle. Absence of a bridge/cap is an empty browser intake. */
export async function takeIntake(id: string, options: {signal?: AbortSignal} = {}): Promise<readonly NativeIntakeItem[]> {
 const bridge = nativeBridge()
 if(!bridge?.caps.intake) return []
 if(!/^[A-Za-z0-9_-]{1,128}$/.test(id)) throw {code: 'invalid_intake'}
 if(options.signal?.aborted) throw {code: 'canceled'}
 let ids = taken.get(bridge)
 if(!ids) {ids = new Set(); taken.set(bridge, ids)}
 if(ids.has(id)) return []
 ids.add(id) // Consume before dispatch, including concurrent calls and uncertain failures.
 return new Promise((resolve, reject) => {
  const dispose = () => {
   window.removeEventListener(NATIVE_EVENT_NAME, listener)
   window.removeEventListener('pagehide', cancel)
   options.signal?.removeEventListener('abort', cancel)
  }
  const cancel = () => {dispose(); reject({code: 'canceled'})}
  const listener = (event: Event) => {
   const detail: unknown = (event as CustomEvent).detail
   if(!isNativeEvent(detail) || detail.type !== 'reply' || detail.id !== id) return
   dispose()
   if(detail.ok) resolve(detail.items)
   else reject(detail.error)
  }
  window.addEventListener(NATIVE_EVENT_NAME, listener)
  window.addEventListener('pagehide', cancel, {once: true})
  options.signal?.addEventListener('abort', cancel, {once: true})
  try {createNativeClient(bridge).post({v: 1, type: 'intake.take', id})} catch {dispose(); reject({code: 'intake_uncertain'})}
 })
}

/** Convert cold URL or warm shell values to the existing Context launch shape. */
export function nativeLaunchParams(values: {link?: string; intake?: string}): LaunchParams {
 const params: Record<string, string> = {}
 if(launchRoute({route: values.link ?? ''}) !== undefined) {params.link = values.link!; params.route = values.link!}
 if(values.intake && /^[A-Za-z0-9_-]{1,128}$/.test(values.intake)) params.intake = values.intake
 return Object.freeze(params)
}

/** Entry/context builders compose this into ctx.onLaunch and dispose it with the context. */
export function onNativeLaunch(listener: (params: LaunchParams) => void): () => void {
 if(!nativeBridge()) return () => {}
 const handler = (event: Event) => {
  const detail: unknown = (event as CustomEvent).detail
  if(!nativeBridge() || !isNativeEvent(detail) || detail.type !== 'launch') return
  const params = nativeLaunchParams(detail)
  if(Object.keys(params).length) listener(params)
 }
 window.addEventListener(NATIVE_EVENT_NAME, handler)
 return () => window.removeEventListener(NATIVE_EVENT_NAME, handler)
}

/** Download progress belongs to the transfer id returned by saveBlob/saveUrl. */
export function onNativeTransfer(listener: (transfer: NativeTransfer) => void): () => void {
 if(!nativeBridge()) return () => {}
 const handler = (event: Event) => {
  const detail: unknown = (event as CustomEvent).detail
  if(nativeBridge() && isNativeEvent(detail) && detail.type === 'transfer') listener(detail)
 }
 window.addEventListener(NATIVE_EVENT_NAME, handler)
 return () => window.removeEventListener(NATIVE_EVENT_NAME, handler)
}

/** A full-page host with native URL routing when injected, ordinary browser behaviour otherwise. */
export function createNativeHost(platform: Partial<HostPlatformSnapshot> = {}): HostAdapter {
 const host = createStandaloneHost(platform)
 return {...host,
  platform: () => ({...host.platform(), ...(nativeBridge() ? {host: 'native-shell' as const} : {})}),
  openURL(url) {
   if(!nativeBridge()?.caps.openExternal) return host.openURL(url)
   try {openExternal(url); return {status: 'requested', windowHandleAvailable: false}}
   catch {return {status: 'invalid-input'}}
  },
 }
}
