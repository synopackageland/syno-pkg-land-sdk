<script setup lang="ts">
// A notice above an App's pages while its executor (ctx.executor) is not
// approved. Unlike ExecutorApproval it holds nothing back: a delegate App's
// pages work, only what needs the executor waits. An administrator approves
// in a dialog (ExecutorSettings); anyone else is told to ask one. A later
// status, or one read on reopen, shows or hides it again. A stopped or
// starting package needs no approval: the notice says so instead and checks
// again on its own, as it does for a status that cannot be read, until the
// executor answers. The setup the App
// provides (ExecutorSetupKey) comes before ctx.executor, and its call before
// ctx.call: the dialog then reads as the App's own settings page does.
import {computed,inject,onBeforeUnmount,onMounted,provide,ref} from 'vue'
import {Button,Callout,Dialog} from '@synopackageland/ui'
import type {Context} from '../context'
import ExecutorSettings from './ExecutorSettings.vue'
import type {CapabilityState,CapabilityStatus} from './executor-setup'
import {ExecutorSetupKey,confirmFailure} from './use-executor-setup'
import {appLifecycle,lifecycleCopy,lifecycleRetryDelay,type AppLifecycle} from './lifecycle'
import {delegateCopy} from './copy'
// purpose is the App's own sentence for what it needs system privileges for.
const props=defineProps<{locale?:string;purpose?:string;ctx:Partial<Pick<Context,'executor'|'user'|'call'|'reopen'>>}>()
const copy=computed(()=>delegateCopy(props.locale))
const setup=inject(ExecutorSetupKey,undefined)??props.ctx.executor
if(setup)provide(ExecutorSetupKey,setup)
const state=ref<CapabilityState|'checking'|'unknown'>('checking'),lifecycle=ref<AppLifecycle>(),open=ref(false)
const admin=computed(()=>props.ctx.user?.isAdmin===true)
const shown=computed(()=>!lifecycle.value&&(state.value==='needs-admin-approval'||state.value==='blocked'))
// ExecutorSettings reads the delegate's file scope through call.
const settingsCtx=computed<Pick<Context,'call'>|undefined>(()=>setup?.call?{call:(method,params)=>setup.call!(method,params)}:props.ctx.call?props.ctx as Pick<Context,'call'>:undefined)
let disposed=false,stopReopen:(()=>void)|undefined,retry:ReturnType<typeof setTimeout>|undefined,attempt=0
// undefined: the status could not be read.
// A stopped package that turns starting starts the backoff over.
function show(next:CapabilityStatus|undefined){
 const was=lifecycle.value
 lifecycle.value=appLifecycle(next)
 state.value=next?.state??'unknown'
 clearTimeout(retry)
 if(next&&!lifecycle.value){attempt=0;return}
 if(lifecycle.value!==was)attempt=0
 retry=setTimeout(()=>{void check()},lifecycleRetryDelay(attempt++,lifecycle.value))
}
async function check(){
 if(!setup)return
 let next:CapabilityStatus|undefined
 try{next=await setup.status()}catch{next=undefined}
 if(!disposed)show(next)
}
onMounted(()=>{
 if(!setup)return
 setup.subscribe?.(next=>{if(!disposed&&!confirmFailure(next))show(next)})
 stopReopen=props.ctx.reopen?.(()=>{void check()})
 void check()
})
onBeforeUnmount(()=>{disposed=true;clearTimeout(retry);stopReopen?.()})
</script>

<template>
  <div v-if="lifecycle" class="executor-notice">
    <Callout tone="info" role="status">{{ lifecycleCopy(lifecycle,locale) }}</Callout>
  </div>
  <div v-else-if="shown" class="executor-notice">
    <Callout tone="warning">
      <span class="executor-notice-body">{{ admin?copy.noticeAdmin:copy.noticeUser }}<Button v-if="admin" variant="secondary" size="sm" @click="open=true">{{ copy.noticeApprove }}</Button></span>
    </Callout>
    <Dialog v-if="admin" :open="open&&shown" :title="copy.setupTitle" @update:open="value=>open=value">
      <ExecutorSettings :admin="true" :locale="locale" :ctx="settingsCtx" :purpose="purpose" title="" @confirmed="check" />
    </Dialog>
  </div>
</template>

<style scoped>
.executor-notice-body { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: var(--ui-space-2, 8px); }
</style>
