// Root-produced diagnostics, read through executor.status. Display only; never a grant.
export interface ApprovalDifference {
 package:string;old_policy_digest:string;new_policy_digest:string;new_executor_digest:string
 permissions:'unchanged'|'expanded'|'review-required';task_template_changed:boolean
 root_programs:Array<{name:string;old:string|null;new:string|null}>
 policy_changes:Array<{path:string;old:unknown;new:unknown}>
}
export type CapabilityState='unsupported'|'needs-admin-approval'|'ready'|'blocked'|'incompatible'
// requested: a scope change whose root task may still land; it applied once
// status() reports that scope with a generation above this one.
// retry: approving again may succeed: nothing of this approval is left behind, or
// the approved tasks remain and approving again restarts the executor.
// code: why, for the App to translate (password_rejected, session_ended,
// scheduler_denied, …); reason is the English detail. outcome: what a failed
// approval's rollback did (restored, withdrawn, restore_failed, withdraw_failed).
// The DSM window's access of an older CLI reports reason only.
export interface CapabilityStatus {state:CapabilityState;approval?:ApprovalDifference;reason?:string;code?:string;outcome?:string;requested?:{scope:string;generation:number};retry?:boolean}
// The App supplies the implementation through app.provide(ExecutorSetupKey, setup).
// Passwords belong only to this call and must never be persisted or logged.
export interface ExecutorSetup {
 // Approved policy profiles provided by the package window for consent details.
 webapiProfiles?:Record<string,{purpose:string;apis:Record<string,Record<string,number[]>>;raw_max_bytes?:number}>
 status():Promise<CapabilityStatus>
 authorize(password:string,selectedScope?:string):Promise<CapabilityStatus>
 control(action:'start'|'stop'|'purge'):Promise<CapabilityStatus>
 // Results of calls made elsewhere (the DSM window panel) on the same access.
 subscribe?(listener:(status:CapabilityStatus)=>void):void
 // The App's call for the approval's own reads (delegate.status; share metadata is read directly from DSM),
 // when its routes differ from the SDK's; ExecutorNotice's dialog uses it in
 // place of ctx.call, as the App's settings page passes it as ctx.
 call?<T=unknown>(method:string,params?:Record<string,unknown>):Promise<T>
}
