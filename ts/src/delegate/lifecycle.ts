// A stopped or starting package: its executor waits for no
// approval and answers again by itself. A call's refusal names it with its
// code; a status names it with the code at the start of its reason (the App's
// delegate.status, and the DSM window's access, whose reason reads
// "APP_STOPPED: the executor status request failed."), or with the window's
// code executor_starting.
import {delegateCopy} from './copy'
export type AppLifecycle='stopped'|'starting'
const codes:Record<string,AppLifecycle>={APP_STOPPED:'stopped',APP_STARTING:'starting',EXECUTOR_STARTING:'starting',executor_starting:'starting'}
export function appLifecycle(value:unknown):AppLifecycle|undefined{
 const {code,reason}=(value??{}) as {code?:unknown;reason?:unknown}
 if(typeof code==='string'&&Object.hasOwn(codes,code))return codes[code]
 const lead=typeof reason==='string'?/^[A-Z_]+(?=:|$)/.exec(reason)?.[0]:undefined
 return lead&&Object.hasOwn(codes,lead)?codes[lead]:undefined
}
// While a panel shows a stopped or starting package it checks again on its
// own: after 2 s, doubling to at most 15 s, or 4 s while it starts, whose end
// is near.
export const LIFECYCLE_RETRY_MAX_MS=15000,LIFECYCLE_STARTING_RETRY_MAX_MS=4000
export function lifecycleRetryDelay(attempt:number,lifecycle?:AppLifecycle):number{
 return Math.min(2000*2**attempt,lifecycle==='starting'?LIFECYCLE_STARTING_RETRY_MAX_MS:LIFECYCLE_RETRY_MAX_MS)
}
// What the SDK says about a stopped or starting package, for an App that
// shows its own refusals. for 'page' (the default): a view that checks again
// on its own, as the SDK's panels do, and says it updates by itself. for
// 'action': an action the package refused (a submit, a save), which nothing
// sends again, and which the user tries again once the package runs.
// The action text for a refusal that names a stopped or starting package, given the locale.
export function lifecycleRefusal(error:unknown,locale?:string):string|undefined{
 const lifecycle=locale===undefined?undefined:appLifecycle(error)
 return lifecycle&&lifecycleCopy(lifecycle,locale,{for:'action'})
}
export interface LifecycleCopyOptions {for?:'page'|'action'}
export function lifecycleCopy(lifecycle:AppLifecycle,locale?:string,options:LifecycleCopyOptions={}):string{
 const copy=delegateCopy(locale)
 if(options.for==='action')return lifecycle==='stopped'?copy.appStoppedAction:copy.appStartingAction
 return lifecycle==='stopped'?copy.appStopped:copy.appStarting
}
