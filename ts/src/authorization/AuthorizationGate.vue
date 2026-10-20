<script setup lang="ts">
import {computed,onMounted,onUnmounted,provide,ref} from 'vue'
import {Button,Callout,Dialog} from '@synopackageland/ui'
import type {AuthorizationStatus,Context} from '../context'
import {authorizationCopy} from './i18n'
import {isAuthorizationDenied} from './failure'
import {appDenial,appRefusal} from './refusal'
import {AuthorizationKey} from './use-authorization'
import {appLifecycle,lifecycleRetryDelay} from '../delegate/lifecycle'
import {useExecutorPreload} from '../delegate/use-executor-setup'
// name fills {package} in the copy; it defaults to ctx.displayName. ctx needs
// only what the Gate reads, so an App that uses only the Gate gives no call or host.
const props=defineProps<{ctx:Pick<Context,'locale'|'displayName'|'reopen'|'authorization'>;name?:string}>()
const emit=defineEmits<{ready:[]}>()
const state=ref<AuthorizationStatus>()
const pendingState=ref<AuthorizationStatus>()
const busy=ref(false)
const failure=ref(false)
// The content never waits: only a status that is not ready opens the dialog.
const open=ref(false)
// Pages ExecutorApproval preloads are hidden: the dialog waits until they show.
const preloading=useExecutorPreload()
let waiters:Array<(ready:boolean)=>void>=[]
let disposed=false,sequence=0,pendingRecheck=false,retry:ReturnType<typeof setTimeout>|undefined,attempt=0
let unsubscribe:(()=>void)|undefined
const text=computed(()=>authorizationCopy(props.ctx.locale,props.name??props.ctx.displayName??''))
const shown=computed(()=>state.value||pendingState.value)
const required=computed(()=>shown.value?.state==='authorization_required')
const untrusted=computed(()=>['helper_untrusted','cli_untrusted'].includes(shown.value?.reason||''))
const canAuthorize=computed(()=>required.value&&!untrusted.value)
// A stopped or starting package, a refused check and a check this page cannot
// read each say what they are; only a temporary failure is called temporary (P14).
const lifecycle=computed(()=>shown.value?.state==='unavailable'?appLifecycle(shown.value):undefined)
const unavailableKey=computed(()=>lifecycle.value??({credential_not_ready:'credential',access_denied:'denied',check_failed:'config'} as Record<string,string>)[shown.value?.reason??'']??'unavailable')
const refusal=computed(()=>appRefusal({code:shown.value?.reason},props.ctx.locale,props.name??props.ctx.displayName))
const title=computed(()=>refusal.value?.title??(!shown.value?text.value('checking'):required.value?text.value('title'):shown.value.state==='ready'?text.value('ready'):shown.value.state==='not_admin'?text.value('not_admin.title'):shown.value.state==='session_expired'?text.value('session_expired.title'):text.value(unavailableKey.value+'.title')))
const body=computed(()=>refusal.value?.message??(required.value?text.value('purpose'):shown.value?.state==='not_admin'?text.value('not_admin.body'):shown.value?.state==='session_expired'?text.value('session_expired.body'):shown.value?.state==='unavailable'?text.value(unavailableKey.value+'.body'):text.value('purpose')))
const reasons=['grant_missing','upgrade_reapproval','helper_untrusted','cli_untrusted','grant_incompatible','package_identity_changed','maintenance_task_missing','maintenance_task_mismatch','helper_health_rejected','capability_missing']
const reason=computed(()=>reasons.includes(shown.value?.reason||'')?shown.value!.reason!:'unknown')
const next=computed(()=>untrusted.value?'next.reinstall':reason.value==='grant_missing'?'next.install':reason.value==='upgrade_reapproval'||reason.value==='grant_incompatible'?'next.update':'next.repair')
function valid(result:AuthorizationStatus|undefined){
 if(!result||result.command!=='authorization'||result.schema_version!==1||!['ready','authorization_required','not_admin','session_expired','unavailable'].includes(result.state))throw Error('invalid status')
 // A reason-only upgrade tombstone never accompanies a usable authorization.
 if(result.reason==='upgrade_reapproval'&&result.state!=='authorization_required')throw Error('invalid status')
 return result
}
const temporary=['service_unavailable','request_timeout','request_canceled','network','app_unavailable','internal','auth_unavailable','EXECUTOR_UNAVAILABLE','EXECUTOR_TIMEOUT','EXECUTOR_PAUSED']
// Why a check failed, as an unavailable status's reason.
function failureReason(e:unknown){
 const {code}=(e??{}) as {code?:unknown}
 const stopped=appLifecycle(e)
 if(stopped)return stopped==='stopped'?'APP_STOPPED':'APP_STARTING'
 if(code==='credential_not_ready')return code
 if(appDenial(e))return appDenial(e)!
 if(isAuthorizationDenied(e))return 'access_denied'
 return typeof code==='string'&&temporary.includes(code)||code===undefined?'service_unavailable':'check_failed'
}
// While the package is stopped or starting the Gate checks again on its own.
// A stopped package that turns starting starts the backoff over.
let checked:typeof lifecycle.value
function recheck(){
 clearTimeout(retry)
 if(lifecycle.value!==checked){checked=lifecycle.value;attempt=0}
 if(!lifecycle.value)return
 retry=setTimeout(()=>{if(busy.value)pendingRecheck=true;else void action('status')},lifecycleRetryDelay(attempt++,lifecycle.value))
}
// O1: under load the first status may be unavailable, so a background check
// retries with backoff before it asks. An expired session and a page without
// DSM's SynoToken are not retried.
const retryDelays=[1000,3000]
async function read(method:'status'|'request'|'revoke'){
 const delays=method==='status'&&!open.value?retryDelays:[]
 for(let attempt=0;;attempt++){
  try{
   const result=valid(await props.ctx.authorization?.[method]())
   if(result.state!=='unavailable'||attempt>=delays.length)return result
  }catch(e){if(['session_required','credential_not_ready'].includes((e as {code?:string}).code!)||attempt>=delays.length)throw e}
  await new Promise(resolve=>setTimeout(resolve,delays[attempt]))
  if(disposed)throw Error('disposed')
 }
}
async function action(method:'status'|'request'|'revoke'){
 if(busy.value||disposed)return
 const current=++sequence
 pendingState.value=method==='request'&&state.value?.state==='authorization_required'?state.value:undefined
 busy.value=true;failure.value=false
 // A request keeps its prior reason visible while DSM's confirmation is pending.
 state.value=undefined
 try {
  const result=await read(method)
  if(disposed||current!==sequence)return
  state.value=result
  open.value=result.state!=='ready'
  if(result.state==='ready')settle(true)
  if(result.state==='ready')emit('ready')
 } catch(e){if(!disposed&&current===sequence){
  state.value=(e as {code?:string}).code==='session_required'?{command:'authorization',schema_version:1,state:'session_expired',reason:'session_expired'}:pendingState.value||{command:'authorization',schema_version:1,state:'unavailable',reason:failureReason(e)}
  // A stopped package, a refusal or a misconfiguration is said by its own body,
  // not as an attempt to repeat.
  failure.value=!refusal.value&&unavailableKey.value==='unavailable';open.value=true
 }} finally{if(!disposed&&current===sequence){busy.value=false;pendingState.value=undefined;if(pendingRecheck){pendingRecheck=false;void action('status')}else recheck()}}
}
function settle(ready:boolean){const pending=waiters;waiters=[];pending.forEach(resolve=>resolve(ready))}
function dismiss(value:boolean){open.value=value;if(!value)settle(false)}
function requireAuthorization(){
 if(state.value?.state==='ready')return Promise.resolve(true)
 // While a check runs, its result decides; a status that is not ready opens the dialog.
 if(!busy.value)open.value=true
 return new Promise<boolean>(resolve=>waiters.push(resolve))
}
provide(AuthorizationKey,{state:computed(()=>state.value?.state??'checking'),requireAuthorization,text,busy,canManage:()=>!!props.ctx.authorization?.canManage?.(),revoke:()=>action('revoke')})
onMounted(()=>{unsubscribe=props.ctx.reopen(()=>{if(busy.value)pendingRecheck=true;else void action('status')});void action('status')})
onUnmounted(()=>{disposed=true;sequence++;clearTimeout(retry);settle(false);if(typeof unsubscribe==='function')unsubscribe()})
</script>
<template>
 <div class="authorization-content"><slot /></div>
 <Dialog :open="open&&!preloading" :title="title" @update:open="dismiss">
  <div class="authorization-view" :aria-busy="busy" :data-authorization-state="state?.state || 'checking'">
   <p class="authorization-body">{{ body }}</p>
   <Callout v-if="required" class="authorization-status" :tone="['grant_missing','upgrade_reapproval'].includes(reason)?'info':'warning'" :title="text('reason.'+reason)">{{ text(next) }}</Callout>
   <Callout v-if="failure" class="authorization-failed" tone="warning">{{ text('failed') }}</Callout>
  </div>
  <template #footer>
   <Button v-if="canAuthorize" variant="primary" :loading="busy" :disabled="busy" @click="action('request')">{{ busy ? text('authorizing') : text('authorize') }}</Button>
   <Button v-else variant="primary" :loading="busy" :disabled="busy" @click="action('status')">{{ busy ? text('checking') : text('retry') }}</Button>
  </template>
 </Dialog>
</template>
<style scoped>
.authorization-view{display:flex;flex-direction:column;gap:var(--ui-space-3,12px)}
.authorization-body{margin:0;font-size:var(--ui-font-size,13px);line-height:var(--ui-line-height,20px);color:var(--ui-text);text-wrap:pretty}
/* Essential recovery text uses the full foreground; DSM's muted token is below 4.5:1. */
.authorization-status,.authorization-failed{color:var(--ui-text)}
.authorization-content{display:flex;flex-direction:column;height:100%;min-height:0}.authorization-content :deep(.app-shell){flex:1;min-height:0;height:auto}
</style>
