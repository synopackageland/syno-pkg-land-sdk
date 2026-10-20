import {inject,ref,type InjectionKey,type Ref} from 'vue'
import type {CapabilityStatus,ExecutorSetup} from './executor-setup'
export const ExecutorSetupKey:InjectionKey<ExecutorSetup>=Symbol('executor-setup')
// Whether ExecutorApproval preloads these pages: mounted, hidden, while the
// executor's first status is read. A page defers what would show outside
// them (a dialog, a toast) until it is false.
export const ExecutorPreloadKey:InjectionKey<Readonly<Ref<boolean>>>=Symbol('executor-preload')
export function useExecutorPreload():Readonly<Ref<boolean>>{return inject(ExecutorPreloadKey,ref(false))}
export function useExecutorSetup():ExecutorSetup {
 return inject(ExecutorSetupKey,{
  async status(){return {state:'blocked',reason:'setup-unavailable'}},
  async authorize(){throw {code:'setup-unavailable'}},
  async control(){throw {code:'setup-unavailable'}},
 })
}
// DSM's PasswordConfirm refused the administrator password, or could not
// confirm it: nothing was created, so the executor still needs approval and the
// result is no status of its own. The DSM window's access reports it in code;
// an older CLI's only in the English reason.
export type ConfirmFailure='password_rejected'|'password_required'|'password_confirm_failed'
const confirmFailures:readonly string[]=['password_rejected','password_required','password_confirm_failed'] satisfies ConfirmFailure[]
export function confirmFailure(status:CapabilityStatus):ConfirmFailure|undefined{
 if(status.state!=='blocked')return undefined
 if(status.code)return confirmFailures.includes(status.code)?status.code as ConfirmFailure:undefined
 const reason=status.reason??''
 if(/^dsm_3121\b/.test(reason))return 'password_rejected'
 if(/^\S+: administrator password confirmation failed\./.test(reason))return 'password_confirm_failed'
 return undefined
}
// DSM refused the administrator password (PasswordConfirm 3121).
export function passwordRejected(status:CapabilityStatus){return confirmFailure(status)==='password_rejected'}
// The DSM window's access got no executor.status answer from the App's CGI
// (as it reports it, "<code>: the executor status request failed.").
export function statusUnreachable(reason?:string){return /: the executor status request failed\.$/.test(reason??'')}
