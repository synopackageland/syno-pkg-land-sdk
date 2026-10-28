import type {AppStatus, Context} from '../context'

export type EntryMode = 'admin-only' | 'app-privilege'
export type EntryFailure = {code: 'session_required' | 'admin_required' | 'app_privilege_denied' | 'auth_unavailable'}
export interface EntryCheck {
 check(): Promise<void>
 dispose(): void
}

/** A background presentation check. The CGI's EntryGate is the authority.
 * A fresh app.status traverses its session and entry checks; ctx.user is
 * display metadata and cannot establish the current session's privileges. */
export function createEntryCheck(
 ctx: Pick<Context, 'call'>,
 mode: EntryMode,
 onResult: (failure: EntryFailure | undefined) => void,
): EntryCheck {
 const abort = new AbortController()
 let pending: Promise<void> | undefined
 async function run() {
  let failure: EntryFailure | undefined
  try {
   const status = await ctx.call<AppStatus>('app.status', {}, {signal: abort.signal})
   if(!status || typeof status.user !== 'string' || !status.user || typeof status.admin !== 'boolean')
    failure = {code: 'auth_unavailable'}
   else if(mode === 'admin-only' && !status.admin) failure = {code: 'admin_required'}
  } catch(error) {
   const code = (error as {code?: unknown})?.code
   failure = {code: code === 'session_required' || code === 'admin_required' || code === 'app_privilege_denied' ? code : 'auth_unavailable'}
  }
  if(!abort.signal.aborted) onResult(failure)
 }
 return {
  check() {
   if(abort.signal.aborted) return Promise.resolve()
   if(!pending) pending = run().finally(() => {pending = undefined})
   return pending
  },
  dispose() {abort.abort()},
 }
}
