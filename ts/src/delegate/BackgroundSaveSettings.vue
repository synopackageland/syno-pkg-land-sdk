<script setup lang="ts">
// One folder row: an administrator approves a scope and grants its root in
// one submit. Ordinary users may choose their own destination within it.
// Only protected executor readback supplies the root used for a grant. Until
// it is approved the section explains why, as ExecutorApproval does.
import {computed,inject,onBeforeUnmount,onMounted,ref} from 'vue'
import {Button,Callout,Form,FormItem,Input,PathDisplay,Select,SettingsRow,SettingsSection,Skeleton} from '@synopackageland/ui'
import type {Context} from '../context'
import type {BackgroundSaveFacade,BackgroundSaveStatus,DestinationGrant,DestinationState} from './long-running'
import {scopedPicker} from './scoped-picker'
import {saveCopy,saveError} from './save-copy'
import {delegateCopy} from './copy'
import {ExecutorSetupKey} from './use-executor-setup'
import {useScopeApproval} from './use-scope-approval'
import {appLifecycle,lifecycleCopy,type AppLifecycle} from './lifecycle'
import {useLifecycleRetry} from './use-lifecycle-retry'
import NasPicker from '../nas/NasPicker.vue'
import ExecutorConsent from './ExecutorConsent.vue'
import type {NasProvider} from '../nas/contract'
const props=defineProps<{sdk:BackgroundSaveFacade;admin:boolean;locale:string
 picker?:{provider:NasProvider;keys:string[]
 // A row's label when an App has several keys; a key itself is never shown.
 labels?:Record<string,string>}
 // The approval's reads (delegate.status; share metadata is read directly from DSM); without it, the call of the setup the App provides.
 ctx?:Pick<Context,'call'|'displayName'>
 // Replaces the section title; an empty string drops it where the page header already names it.
 title?:string
 // The App's own text for a refused folder choice; undefined keeps the SDK's.
 pickerErrorText?:(error:unknown)=>string|undefined}>()
// destinations: each picker key's state after every read, the App's own record of where saves go.
const emit=defineEmits<{authorized:[grant:DestinationGrant];confirmed:[];destinations:[states:DestinationState[]]}>()
const copy=computed(()=>saveCopy(props.locale)),executorCopy=computed(()=>delegateCopy(props.locale))
const setup=inject(ExecutorSetupKey,undefined)
const approvalCtx=computed(()=>props.ctx??(setup?.call?{call:setup.call.bind(setup)} as Pick<Context,'call'>:undefined))
const approval=useScopeApproval({get admin(){return props.admin},get locale(){return props.locale},get ctx(){return approvalCtx.value}},approved)
const {password,busy:approving,confirming,failure,passwordError,scope,chosen,options,dynamic,canConfirm}=approval
const status=ref<BackgroundSaveStatus>(),destinations=ref<DestinationState[]>([]),unapproved=ref(false)
const busy=ref(false),error=ref<unknown>(),choiceRefused=ref(false),changing=ref(false),picking=ref(false),destinationKey=ref('')
// The App's refusal names a stopped or starting package; an App that reads it
// as unapproved leaves it to the window's status.
const refusedLifecycle=ref<AppLifecycle>()
const lifecycle=computed(()=>refusedLifecycle.value??(unapproved.value?appLifecycle(approval.status.value):undefined))
let disposed=false
async function load(){
 let current:BackgroundSaveStatus
 try{current=await props.sdk.status()}
 catch(e){
  const stopped=appLifecycle(e)
  if(!stopped&&(e as {code?:string})?.code!=='EXECUTOR_UNAVAILABLE')throw e
  if(!disposed){refusedLifecycle.value=stopped;unapproved.value=!stopped;status.value=undefined;destinations.value=[]}
  return
 }
 const choices=await Promise.all((props.picker?.keys??[]).map(key=>props.sdk.destination(key)))
 if(disposed)return
 refusedLifecycle.value=undefined;unapproved.value=false;status.value=current;destinations.value=choices
 emit('destinations',choices)
}
async function action(work:()=>Promise<void>){
 if(disposed||busy.value)return
 busy.value=true;error.value=undefined;choiceRefused.value=false
 try{await work()}
 catch(e){if(!disposed)error.value=e}
 finally{if(!disposed)busy.value=false}
}
const reload=()=>action(load)
const refresh=()=>Promise.all([reload(),props.admin?approval.refresh():undefined]).then(()=>{})
function authorized(grant:DestinationGrant){emit('authorized',grant);void reload()}
function refused(e:unknown){if(!disposed){error.value=e;choiceRefused.value=true}}
const errorMessage=computed(()=>choiceRefused.value?props.pickerErrorText?.(error.value)??saveError(error.value,copy.value,'select',props.locale,props.ctx?.displayName):saveError(error.value,copy.value,'save',props.locale,props.ctx?.displayName))
const revoke=(id:string)=>action(async()=>{if((await props.sdk.revoke({delegation_id:id}))?.revoked!==true)throw {code:'commit-uncertain'};await load()})
const enable=()=>action(async()=>{if(!props.admin)return;if((await props.sdk.enable())?.disabled!==false)throw {code:'commit-uncertain'};await load()})
// Approval is not a grant. Read the executor back, then use the same session-
// checked destination API as NasPicker; a refusal never becomes success.
async function approved(){
 await action(async()=>{
  if(disposed)return
  const current=await props.sdk.status(),approved=scope.value?.selection
  if(disposed)return
  if(dynamic.value){
   const root=current.selection
   if(!approved||!root||root.scope!==chosen.value||root.scope!==approved.scope||root.generation!==approved.generation||root.root!==approved.root)throw {code:'commit-uncertain'}
   const keys=destinationKey.value?[destinationKey.value]:props.picker?.keys??[]
   for(const key of keys){
    if(disposed)return
    let grant:DestinationGrant
    try{grant=await props.sdk.selectDestination({key,path:root.root})}catch(e){choiceRefused.value=true;throw e}
    if(!grant?.id||!grant.username||grant.path!==root.root||grant.key!==key||grant.lifetime!=='until-revoked')throw {code:'commit-uncertain'}
    if(disposed)return
    emit('authorized',grant)
   }
  }
  if(disposed)return
  changing.value=false;emit('confirmed');await load()
 })
}
function choose(key:string){destinationKey.value=key;if(props.admin&&dynamic.value)changing.value=true;else picking.value=true}
const selection=computed(()=>status.value?.selection)
const reading=computed(()=>!status.value&&!unapproved.value&&!error.value&&!lifecycle.value)
// Not approved, or approved without the scope an install-selected executor needs.
const needsApproval=computed(()=>unapproved.value&&(!props.admin||approval.status.value?.state!=='blocked')||status.value?.scopeMode==='install-selected'&&!selection.value)
const blocked=computed(()=>props.admin&&unapproved.value&&approval.status.value?.state==='blocked'&&!approval.status.value.retry)
const ready=computed(()=>!!status.value&&!needsApproval.value&&!status.value.disabled)
const approvingForm=computed(()=>props.admin&&(needsApproval.value||unapproved.value&&!!approval.status.value?.retry))
const pickerProvider=computed(()=>!props.picker?undefined:selection.value?scopedPicker(props.picker.provider,selection.value.root):status.value?.scopeMode==='install-selected'?undefined:props.picker.provider)
const rowLabel=(key:string)=>props.picker?.labels?.[key]??copy.value.folder
// A refused password shows at its field; any other failure above the form.
const failureText=computed(()=>{const f=failure.value;if(!f||'confirm' in f)return '';return f.code==='session_ended'?executorCopy.value.session:approval.explain(f)??executorCopy.value.setupError})
// What approving explains: a first approval, not a change of folder.
const consent=computed(()=>props.admin&&needsApproval.value&&!changing.value)
const submitDisabled=computed(()=>approving.value||busy.value||approval.error.value||!canConfirm.value)
function cancelChange(){changing.value=false;password.value='';failure.value=undefined;chosen.value=scope.value?.selection?.scope??''}
useLifecycleRetry(()=>lifecycle.value,()=>Promise.all([reload(),props.admin?approval.refresh():undefined]))
// The page header's refresh reloads every panel on the page.
defineExpose({refresh})
onMounted(()=>{void reload()})
onBeforeUnmount(()=>{disposed=true})
</script>
<template>
 <SettingsSection :title="title??copy.title" :description="needsApproval&&!changing?undefined:copy.help" :collapsible="false" :aria-busy="busy||approving||reading" class="background-save-settings">
  <template v-if="reading">
   <SettingsRow v-for="key in picker?.keys??[]" :key="key" :label="rowLabel(key)" field-width="full" data-placeholder-row><Skeleton width="20rem" height="28px" /></SettingsRow>
  </template>
  <Callout v-else-if="lifecycle" tone="info" role="status">{{ lifecycleCopy(lifecycle,locale) }}</Callout>
  <template v-else>
  <Callout v-if="error" tone="danger" role="alert"><span class="save-alert"><span data-save-error>{{ errorMessage }}</span><Button variant="secondary" size="sm" :disabled="busy" @click="reload">{{ copy.retry }}</Button></span></Callout>
  <Callout v-if="approval.error.value" tone="danger" role="alert"><span class="save-alert">{{ executorCopy.setupError }}<Button variant="secondary" size="sm" :disabled="approving" @click="approval.refresh">{{ copy.retry }}</Button></span></Callout>
  <Callout v-if="failureText" tone="danger" role="alert">{{ failureText }}<span v-if="failure&&'reason' in failure&&failure.reason" class="save-reason">{{ failure.reason }}</span></Callout>
  <Callout v-if="blocked" tone="warning">{{ approval.explain(approval.status.value)??executorCopy.blocked }}<span v-if="approval.status.value?.reason" class="save-reason">{{ approval.status.value.reason }}</span></Callout>
  <ExecutorConsent v-else-if="needsApproval&&!admin" :admin="false" :locale="locale" files />
  <Callout v-if="status?.disabled" tone="warning"><span class="save-alert"><span>{{ copy.disabled }}</span><Button v-if="admin" variant="secondary" size="sm" :disabled="busy" @click="enable">{{ copy.enable }}</Button></span></Callout>
  <Callout v-if="ready&&destinations.some(d=>d.status==='reselection-required')" tone="warning">{{ copy.reselect }}</Callout>

  <component :is="consent?ExecutorConsent:'div'" v-if="approvingForm||changing" v-bind="consent?{admin:true,locale,files:true,status:approval.status.value}:{}">
   <Form direction="vertical" class="save-form" @submit.prevent="approval.submit">
    <p v-if="changing" class="save-help">{{ copy.changeHelp }}</p>
    <FormItem v-if="dynamic" :label="copy.folder" :description="consent?copy.approveHelp:undefined" data-save-row="folder"><Select id="save-scope" v-model="chosen" :options="options" :disabled="approving" /></FormItem>
    <FormItem :label="copy.password" :description="confirming?executorCopy.confirmingHelp:consent?executorCopy.passwordNote:undefined" :help="passwordError" :validate-status="passwordError?'error':undefined" validate-message-display-mode="text">
     <Input id="save-password" v-model="password" type="password" autocomplete="current-password" :invalid="!!passwordError" :disabled="approving" />
     <template #after="{classes}"><Button :class="classes.afterItem" type="submit" variant="primary" :loading="confirming" :disabled="submitDisabled">{{ changing?copy.confirmChange:copy.approve }}</Button><Button v-if="changing" :class="classes.afterItem" type="button" variant="secondary" :disabled="approving" @click="cancelChange">{{ copy.cancelChange }}</Button></template>
    </FormItem>
   </Form>
  </component>
  <template v-if="ready&&!changing&&!approvingForm">
   <SettingsRow v-for="d in destinations" :key="d.key" :label="rowLabel(d.key)" field-width="full" data-save-row="folder">
    <div class="save-actions">
     <PathDisplay v-if="d.status==='active'&&d.destination" :path="d.destination.path" /><span v-else class="save-none">{{ copy.folderNone }}</span>
     <Button variant="secondary" size="sm" :disabled="busy||approving||(admin&&status?.scopeMode==='install-selected'?!dynamic||approval.error.value:!pickerProvider)" @click="choose(d.key)">{{ copy.choose }}</Button>
     <Button v-if="d.status==='active'&&d.destination" variant="ghost" size="sm" :disabled="busy" @click="revoke(d.destination.id)">{{ copy.revoke }}</Button>
    </div>
   </SettingsRow>
  </template>
  </template>
  <slot />
  <NasPicker v-if="pickerProvider" :key="selection?.scope" v-model:open="picking" :provider="pickerProvider" :locale="locale" :name="ctx?.displayName" mode="folder" :destination="{key:destinationKey,selectDestination:sdk.selectDestination}" @authorized="authorized" @refused="refused" :error-text="pickerErrorText" />

 </SettingsSection>
</template>
<style scoped>
.background-save-settings {min-width:0;overflow-wrap:anywhere;}
.save-help {margin:0 0 var(--ui-space-2,8px);max-width:72ch;color:var(--ui-text-secondary);}
.save-form {max-width:28rem;}
.save-actions,.save-alert {display:flex;align-items:center;flex-wrap:wrap;gap:var(--ui-space-2,8px);}
.save-alert {justify-content:space-between;}
.save-actions :deep(.ui-path-display) {flex:1;min-width:0;}
.save-none {flex:1;color:var(--ui-text-secondary);}
.save-reason {display:block;margin-top:var(--ui-space-1,4px);font-size:var(--ui-font-size-sm,12px);}
</style>
