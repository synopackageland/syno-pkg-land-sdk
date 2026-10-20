import {computed,ref} from 'vue'
import type {Context, CallOptions} from '../context'
import type {BatchCall, BatchResult} from '../client'
import type {IdentityNotice} from './i18n'
import {appDenial,appRefusal,type AppDenial} from '../authorization/refusal'

export interface SessionSubject {user: string; uid: string; expiresAt: string}
export function createSession(ctx: Context) {
 const deniedCode = ref<AppDenial>()
 const denied = computed(() => deniedCode.value ? {code:deniedCode.value,...appRefusal({code:deniedCode.value},ctx.locale,ctx.displayName)!} : undefined)
 const enabled = ref(false)
 const ready = ref(false)
 const subject = ref<SessionSubject>()
 const notice = ref<IdentityNotice>('')
 let disposed = false
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
 async function start() {
  try {
   const status = await identityCall<{enabled: boolean}>('identity.status')
   if (disposed) return
   deniedCode.value = undefined
   enabled.value = status.enabled === true
   if (enabled.value) {
    try {accept(await identityCall<SessionSubject>('identity.session', {bootstrap: true}))}
    catch(e) {if (!disposed && !appDenial(e)) clear()}
   }
  } catch (e) {
   if(disposed)return
   // Existing templates have no identity transport and retain their DSM shell.
   const code = (e as {code?: string})?.code
   enabled.value = !appDenial(e) && !['unknown_method', 'session_required'].includes(code || '')
   if (enabled.value) notice.value = 'unavailable'
  } finally {if (!disposed) ready.value = true}
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
 function dispose() {disposed = true; clear()}
 return {enabled, ready, subject, notice, denied, start, call, batch, login, bootstrap, logout, dispose}
}
export type IdentitySession = ReturnType<typeof createSession>
