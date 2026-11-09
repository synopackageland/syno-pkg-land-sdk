import {computed,ref} from 'vue'
import type {Context, CallOptions} from '../context'
import type {BatchCall, BatchResult} from '../client'
import type {IdentityNotice} from './i18n'
import {appDenial,appRefusal,type AppDenial} from '../authorization/refusal'
import {readDsmHttpsURL} from '../dsm/https'

export interface SessionSubject {user: string; uid: string; expiresAt: string}
export interface SessionOptions {
 /** false: this App never uses standalone identity (DSM session only); skip the identity.status probe. */
 identity?: boolean
 /** Delays (ms) between automatic re-probes after a failed identity.status. Default 2s, 5s, 15s, 30s. */
 retryDelays?: readonly number[]
}
const defaultRetryDelays = [2000, 5000, 15000, 30000] as const
export function createSession(ctx: Context, options: SessionOptions = {}) {
 const deniedCode = ref<AppDenial>()
 const denied = computed(() => deniedCode.value ? {code:deniedCode.value,...appRefusal({code:deniedCode.value},ctx.locale,ctx.displayName)!} : undefined)
 const enabled = ref(false)
 const ready = ref(false)
 const subject = ref<SessionSubject>()
 const notice = ref<IdentityNotice>('')
 // identity.status could not be read (timeout, 5xx, network, restarting). Never means "enabled".
 const unavailable = ref(false)
 let disposed = false
 let probe: ReturnType<typeof setTimeout> | undefined
 let probing: Promise<void> | undefined
 let attempt = 0
 let revision = 0
 let expiry: ReturnType<typeof setTimeout> | undefined
 let poll: ReturnType<typeof setTimeout> | undefined
 function cancelTimers() {clearTimeout(expiry); clearTimeout(poll)}
 function clear(message: IdentityNotice = '') {revision++; cancelTimers(); subject.value = undefined; notice.value = message}
 function recordDenial(error:unknown) {
  const code=appDenial(error)
  if(disposed||!code)return false
  clear();deniedCode.value=code;return true
 }
 async function identityCall<T>(method:string,params?:Record<string,unknown>):Promise<T> {
  const current=revision
  try{return await ctx.call<T>(method,params)}catch(e){if(current===revision)recordDenial(e);throw e}
 }
 function accept(value: SessionSubject) {
  if (disposed) return
  const remaining = Date.parse(value.expiresAt) - Date.now()
  if (!Number.isFinite(remaining) || remaining <= 0) {clear('expired'); return}
  cancelTimers(); deniedCode.value = undefined; subject.value = value; notice.value = ''
  expiry = setTimeout(() => clear('expired'), remaining)
  const current = revision
  poll = setTimeout(async () => {
   try {const next = await identityCall<SessionSubject>('identity.session'); if (!disposed && revision === current) accept(next)}
   catch(e) {if (!disposed && revision === current && !recordDenial(e)) clear('expired')}
  }, Math.min(60000, remaining))
 }
 async function status() {
  clearTimeout(probe)
  try {
   const status = await identityCall<{enabled: boolean}>('identity.status')
   if (disposed) return
   deniedCode.value = undefined; unavailable.value = false; attempt = 0
   if (notice.value === 'unavailable') notice.value = ''
   enabled.value = status.enabled === true
   if (enabled.value) {
    try {accept(await identityCall<SessionSubject>('identity.session', {bootstrap: true}))}
    catch(e) {if (!disposed && !appDenial(e)) clear()}
   }
  } catch (e) {
   if(disposed)return
   const code = (e as {code?: string})?.code
   // Existing templates have no identity transport and retain their DSM shell;
   // a refusal keeps the DSM shell with its denial.
   if (appDenial(e) || ['unknown_method', 'session_required'].includes(code || '')) {unavailable.value = false; return}
   // Any other failure is not an answer: keep the App login off (fail closed to
   // the DSM shell; the backend still enforces its own session) and re-probe.
   unavailable.value = true
   if (!enabled.value) notice.value = 'unavailable'
   const delays = options.retryDelays ?? defaultRetryDelays
   if (attempt < delays.length) probe = setTimeout(() => {void retry()}, delays[attempt++])
  } finally {if (!disposed) ready.value = true}
 }
 function run() {
  if (!probing) probing = status().finally(() => {probing = undefined})
  return probing
 }
 async function start() {
  if (options.identity === false) {ready.value = true; return}
  attempt = 0
  return run()
 }
 /** Read identity.status again now (e.g. a Retry button while unavailable). */
 function retry() {
  if (disposed || options.identity === false) return Promise.resolve()
  return run()
 }
 async function call<T = unknown>(method: string, params?: Record<string, unknown>): Promise<T> {
  const current = revision
  try {
   const result = await ctx.call<T>(method, params)
   if (disposed || (enabled.value && current !== revision)) throw {code: 'session_required'}
   return result
  }
  catch (e) {
   if(current === revision)recordDenial(e)
   const code = (e as {code?: string})?.code
   if (!disposed && current === revision && enabled.value && ['session_required', 'session_revalidation_required'].includes(code || '')) {
    clear(code === 'session_revalidation_required' ? 'revalidate' : 'expired')
   }
   throw e
  }
 }
 async function batch<T = unknown>(calls: readonly BatchCall[], options?: CallOptions): Promise<BatchResult<T>[]> {
  if(!ctx.batch) throw {code:'unsupported',message:'batch client unavailable'}
  const current=revision
  try {
   const results=await ctx.batch<T>(calls,options)
   if(disposed || (enabled.value && current!==revision)) throw {code:'session_required'}
   for(const result of results) if(!result.success) {
    if(current===revision) recordDenial(result.error)
    if(current===revision && enabled.value && ['session_required','session_revalidation_required'].includes(result.error.code)) clear(result.error.code==='session_revalidation_required'?'revalidate':'expired')
   }
   return results
  } catch(error) {if(current===revision)recordDenial(error);throw error}
 }
 async function login(account: string, password: string, otp: string) {
  const value = await identityCall<SessionSubject>('identity.login', {account, password, otp})
  if (!disposed) {revision++; accept(value)}
 }
 async function bootstrap() {
  const value = await identityCall<SessionSubject>('identity.session', {bootstrap: true})
  if (!disposed) {revision++; accept(value)}
 }
 async function logout() {
  await identityCall('identity.logout'); if (!disposed) clear()
 }
 function dispose() {disposed = true; clearTimeout(probe); clear()}
 /** Best-effort HTTPS DSM link for LoginPage; unknown/ordinary users never query PortInfo. */
 function readHttpsURL(pageURL: string, signal?: AbortSignal) {
  return readDsmHttpsURL(ctx, pageURL, {signal, keepLaunchApp: true})
 }
 return {enabled, ready, unavailable, subject, notice, denied, start, retry, call, batch, login, bootstrap, logout, dispose, readHttpsURL}
}
export type IdentitySession = ReturnType<typeof createSession>
