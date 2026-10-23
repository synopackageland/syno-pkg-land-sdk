// The administrator's approval of the executor and its file scope: the
// executor status, the shares to choose from, the user password and why the
// last approval failed. ExecutorSettings and BackgroundSaveSettings lay it out.
import {computed,onBeforeUnmount,onMounted,ref,watch} from 'vue'
import type {Context} from '../context'
import type {ScopeSelection} from './long-running'
import type {CapabilityStatus} from './executor-setup'
import {confirmFailure,statusUnreachable,useExecutorSetup,type ConfirmFailure} from './use-executor-setup'
import {appLifecycle} from './lifecycle'
import {delegateCopy} from './copy'
import {createDsmFileStation} from '../dsm/filestation'
export interface ScopeDetail {scope_mode?:string;preselection?:string;selection?:ScopeSelection|null;mismatch?:boolean}
// The password field's text for each confirmation DSM refused or could not make.
const confirmCopy={password_rejected:'passwordRejected',password_required:'passwordRequired',password_confirm_failed:'passwordConfirmFailed'} as const
// How long a requested scope is read back for, and how often.
export const SCOPE_READBACK_MS=60000,SCOPE_READBACK_POLL_MS=1000
// initial: a status the caller has just read, which the first load starts from.
export function useScopeApproval(props:{readonly admin:boolean;readonly locale?:string;readonly ctx?:Pick<Context,'call'>;readonly initial?:CapabilityStatus},confirmed:()=>void|Promise<void>){
 const setup=useExecutorSetup(),copy=computed(()=>delegateCopy(props.locale))
 const status=ref<CapabilityStatus>(),password=ref(''),busy=ref(false),confirming=ref(false),error=ref(false)
 // Why the last approval failed: a refused password, or the executor's
 // reason. It stays until the administrator submits again or refreshes.
 const failure=ref<{confirm:ConfirmFailure}|{reason:string;code?:string}>()
 // The password field's own error: none was entered (until one is typed), or
 // DSM refused or could not confirm it (until the next submit, the regression test).
 const missing=ref(false)
 const passwordError=computed(()=>missing.value?copy.value.passwordRequired:failure.value&&'confirm' in failure.value?copy.value[confirmCopy[failure.value.confirm]]:'')
 watch(password,()=>{missing.value=false})
 const scope=ref<ScopeDetail>(),chosen=ref(''),shares=ref<Array<{label:string;value:string}>>([])
 const dsmFiles=createDsmFileStation()
 const dynamic=computed(()=>scope.value?.scope_mode==='install-selected')
 // Only an executor that reports a file scope is spoken of as file access; a
 // root-ops App's executor gets the neutral copy.
 const files=computed(()=>!!scope.value?.scope_mode)
 const options=computed(()=>[{label:copy.value.homeScope,value:'home'},...shares.value])
 const canConfirm=computed(()=>!dynamic.value||!!chosen.value&&options.value.some(o=>o.value===chosen.value))
 const needsScope=computed(()=>dynamic.value&&!scope.value?.selection)
 let disposed=false
 async function load(approved?:CapabilityStatus){
  const next=approved??await setup.status()
  if(disposed)return
  status.value=next
  // A stopped or starting package has no file scope to read.
  if(!props.ctx||appLifecycle(next))return
  const detail=await props.ctx.call<ScopeDetail>('delegate.status')
  if(disposed)return
  scope.value=detail
  if(detail.scope_mode!=='install-selected')return
  const all:Array<{label:string;value:string}>=[]
  let offset=0
  for(let count=0;count<128;count++){
   const page=await dsmFiles.shares(offset,new AbortController().signal) as {shares:Array<{name:string;path:string;isdir:boolean}>;total:number;offset:number}
   if(disposed)return
   if(!Array.isArray(page.shares)||!Number.isSafeInteger(page.total)||page.total<0||!page.shares.length&&offset<page.total)throw {code:'EXECUTOR_PROTOCOL'}
   for(const entry of page.shares){if(entry.isdir&&entry.path==='/'+entry.name&&!['home','homes'].includes(entry.name.toLowerCase()))all.push({label:entry.name,value:'shared:'+entry.name})}
   offset+=page.shares.length
   if(offset>=page.total)break
   if(count===127)throw {code:'EXECUTOR_PROTOCOL'}
  }
  shares.value=all
  chosen.value=detail.selection?.scope??detail.preselection??''
 }
 async function reload(approved?:CapabilityStatus){if(!props.admin||busy.value)return;busy.value=true;error.value=false;failure.value=undefined;try{await load(approved)}catch{if(!disposed)error.value=true}finally{if(!disposed)busy.value=false}}
 const refresh=()=>reload()
 async function authorize(){
  if(!props.admin||busy.value||!password.value||!canConfirm.value||error.value)return
  busy.value=true;error.value=false;failure.value=undefined;const entered=password.value,requestedScope=chosen.value;password.value=''
  confirming.value=true
  try{
   let next=dynamic.value?await setup.authorize(entered,chosen.value):await setup.authorize(entered)
   const refused=confirmFailure(next)
   // What was typed stays for another try; it is never sent again by itself.
   if(refused){if(!disposed){failure.value={confirm:refused};password.value=entered}return}
   await load(next)
   // After a reinstall the approval can end before the share is confirmed;
   // confirm it in this submit. A refused password is never sent again.
   if(!disposed&&dynamic.value&&next.state==='ready'&&needsScope.value){next=await setup.authorize(entered,chosen.value);await load(next)}
   // The window can stop waiting before the scope's root task lands: read it back before calling it failed.
   if(next.state!=='ready'&&next.requested&&await landed(next.requested)){next=await setup.status();await load(next)}
   // Approved, yet the executor did not come up: say so instead of showing the form again.
   if(next.state==='ready'&&dynamic.value&&scope.value?.selection?.scope!==requestedScope){if(!disposed)failure.value={reason:'',code:'selection_failed'};return}
   if(next.state!=='ready'){if(!disposed)failure.value={reason:next.reason??'',code:next.code};return}
   if(!disposed)await confirmed()
  }
  catch{if(!disposed)error.value=true}
  finally{if(!disposed){busy.value=false;confirming.value=false}}
 }
 // An empty password is said at its field instead of disabling the button.
 function submit(){if(!password.value&&!busy.value){missing.value=true;return Promise.resolve()}return authorize()}
 // Applied once delegate.status shows the requested scope above the generation it replaced.
 async function landed(requested:{scope:string;generation:number}){
  for(const deadline=Date.now()+SCOPE_READBACK_MS;!disposed;await new Promise(r=>setTimeout(r,SCOPE_READBACK_POLL_MS))){
   const selection=(await props.ctx?.call<ScopeDetail>('delegate.status').catch(()=>undefined))?.selection
   if(selection?.scope===requested.scope&&selection.generation>requested.generation)return true
   if(Date.now()>=deadline)return false
  }
  return false
 }
 // Copy that explains a blocked reason the window's access names; any other reason is shown as it is.
 const explain=(r?:{reason?:string;code?:string})=>r?.code==='executor_starting'?copy.value.statusStarting:statusUnreachable(r?.reason)?copy.value.statusUnreachable:undefined
 onMounted(()=>{setup.subscribe?.(next=>{if(!disposed&&!confirmFailure(next))status.value=next});void reload(props.initial)})
 onBeforeUnmount(()=>{disposed=true;password.value=''})
 return {status,password,busy,confirming,error,failure,passwordError,scope,chosen,options,dynamic,files,webapiProfiles:setup.webapiProfiles,canConfirm,needsScope,refresh,authorize,submit,explain}
}
