import type {Context} from '../context'
import type {Capability,DelegateGrant,MoveMirror} from './dtos-gen'
import type {BackgroundSaveFacade,DestinationGrant,DestinationState,ScopeSelection,OperationRef,OperationResult,SaveOperation} from './long-running'
// Supply an owner-bound App backend view, not browser storage or an executor list.
// Producers persist these references alongside their own business job metadata.
export interface AppSaveJob extends Pick<MoveMirror,'job_id'|'delegation_id'|'name'|'refusal'> {}
const token=/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/
const id=(value:unknown):value is string=>typeof value==='string'&&token.test(value)
const validRef=(ref:OperationRef)=>ref&&id(ref.delegation_id)&&id(ref.job_id)
const path=(value:unknown):value is string=>typeof value==='string'&&value.startsWith('/')&&value!=='/'&&!value.includes('\0')&&!value.slice(1).split('/').some(part=>!part||part==='.'||part==='..')
function protocol():never{throw {code:'EXECUTOR_PROTOCOL'}}
function validSelection(value:ScopeSelection){return value&&Number.isSafeInteger(value.generation)&&value.generation>=0&&path(value.root)&&(value.scope==='home'?value.root==='/home':value.scope.startsWith('shared:')&&value.root==='/'+value.scope.slice(7))}
function result(value:OperationResult,ref:OperationRef):OperationResult{
 if(!value||value.job_id!==ref.job_id||!['queued','copying','committed','canceled','interrupted','uncertain'].includes(value.state))protocol()
 if(value.state==='committed'){
  if(!value.receipt||!path(value.receipt.path)||!Number.isSafeInteger(value.receipt.size)||value.receipt.size<0||value.receipt.size>16*1024**3)protocol()
 }else if(value.receipt!==undefined)protocol()
 if(value.reason!==undefined&&(typeof value.reason!=='string'||!/^[A-Za-z][A-Za-z0-9_-]{0,127}$/.test(value.reason)))protocol()
 return {job_id:value.job_id,state:value.state,...(value.receipt?{receipt:{...value.receipt}}:{}),...(value.code?{code:value.code}:{}),...(value.reason?{reason:value.reason}:{})}
}
/** Bind the UI facade to the App backend's authenticated delegate.* routes (Go package delegate). */
export function createBackgroundSaveFacade(ctx:Pick<Context,'call'>,jobs:()=>Promise<readonly AppSaveJob[]>=async()=>{
 const out=await ctx.call<{operations:MoveMirror[]}>('delegate.operations.list')
 if(!Array.isArray(out?.operations))protocol()
 return out.operations
}):BackgroundSaveFacade{
 const operation=async(method:string,ref:OperationRef)=>{
  if(!validRef(ref))protocol()
  return result(await ctx.call<OperationResult>(method,{delegation_id:ref.delegation_id,job_id:ref.job_id}),ref)
 }
 return {
  async destination(key){
   if(!id(key))protocol()
   const out=await ctx.call<DestinationState>('delegate.destination',{key})
   if(!out||out.key!==key||!['active','missing','revoked','reselection-required','needs-scope-confirmation','disabled'].includes(out.status)||out.selection!==null&&!validSelection(out.selection))protocol()
   if(out.status==='active'){
    const g=out.destination,s=out.selection
    if(!g||!s||!id(g.id)||g.key!==key||g.lifetime!=='until-revoked'||!g.username||!path(g.path)||!(g.path===s.root||g.path.startsWith(s.root+'/')))protocol()
   }else if(out.destination!==null)protocol()
   return out
  },
  async exportExample(input){
   if(!id(input.key)||!id(input.job_id))protocol()
   const out=await ctx.call<OperationRef>('delegate.example.export',{key:input.key,job_id:input.job_id})
   if(!validRef(out)||out.job_id!==input.job_id)protocol()
   return out
  },
  selectDestination:input=>ctx.call<DestinationGrant>('delegate.select_destination',{key:input.key,path:input.path}),
  async status(){
   const value=await ctx.call<Capability>('delegate.status')
   // Capability readiness and the protected admission switch are independent.
   if(!['ready','disabled','needs-scope-confirmation'].includes(value?.state))throw {code:'EXECUTOR_UNAVAILABLE'}
   if(typeof value?.disabled!=='boolean')protocol()
   if(value.selection&&!validSelection(value.selection))protocol()
   return {disabled:value.disabled,...(value.scope_mode?{scopeMode:value.scope_mode,selection:value.selection??null,preselection:value.preselection,mismatch:value.mismatch}:{} )}
  },
  async list(){
   const value=await ctx.call<{delegations:DelegateGrant[]}>('delegate.list')
   if(!Array.isArray(value?.delegations))protocol()
   return value.delegations.filter(g=>g.lifetime==='until-revoked'&&!g.revoked).map(g=>{
    if(!id(g.delegation_id)||typeof g.destination!=='string'||!g.destination||!path(g.path)||typeof g.subject?.username!=='string'||!g.subject.username)protocol()
    return {id:g.delegation_id,key:g.destination,path:g.path,username:g.subject.username,lifetime:'until-revoked' as const}
   })
  },
  async operations(){
   const metadata=await jobs()
   return Promise.all(metadata.map(async job=>{
    if(typeof job?.name!=='string'||!job.name||!validRef(job))protocol()
    const ref={delegation_id:job.delegation_id,job_id:job.job_id}
    try{return {...ref,name:job.name,...await operation('delegate.operations.get',ref)} satisfies SaveOperation}
    catch(e){
     if(job.refusal&&(e as {code?:string})?.code==='operation-not-found')return {...ref,name:job.name,state:'interrupted',code:job.refusal} satisfies SaveOperation
     throw e
    }
   }))
  },
  async revoke(input){
   const out=await ctx.call<{revoked:boolean}>('delegate.revoke',{delegation_id:input.delegation_id})
   if(out?.revoked!==true)protocol()
   return {revoked:true}
  },
  cancel:ref=>operation('delegate.operations.cancel',ref),
  async disable(){const out=await ctx.call<{disabled:boolean}>('delegate.disable');if(out?.disabled!==true)protocol();return {disabled:true}},
  async enable(){const out=await ctx.call<{disabled:boolean}>('delegate.enable');if(out?.disabled!==false)protocol();return {disabled:false}},
 }
}
