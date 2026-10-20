<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref,watch} from 'vue'
import {Badge,Button,Callout,Form,FormActions,FormItem,Input,ListItem,PathDisplay,SettingsSection,Skeleton} from '@synopackageland/ui'
import type {Context} from '../context'
import NasPicker from '../nas/NasPicker.vue'
import {createNasProvider} from '../nas/provider'
import type {CapabilityStatus} from './executor-setup'
import {delegateCopy,errorCopy} from './copy'
import {appLifecycle,lifecycleCopy,lifecycleRefusal,type AppLifecycle} from './lifecycle'
import {useLifecycleRetry} from './use-lifecycle-retry'
import {labelSeparator} from '../i18n'
interface Grant{delegation_id:string;job_id:string;path:string;expires_at:string;revoked?:boolean}
interface Job{id:string;name:string;state:'waiting_executor'|'queued'|'running'|'succeeded'|'failed'|'canceled'|'interrupted'}
const props=defineProps<{ctx:Pick<Context,'call'|'locale'|'displayName'>}>()
const copy=computed(()=>delegateCopy(props.ctx.locale)),separator=computed(()=>labelSeparator(props.ctx.locale)),provider=createNasProvider(props.ctx)
const status=ref<CapabilityStatus>(),grants=ref<Grant[]>([]),jobs=ref<Job[]>([]),busy=ref(false),error=ref<unknown>(),notice=ref(''),picking=ref(false),dest=ref(''),url=ref(''),name=ref('')
let disposed=false,timer:ReturnType<typeof setInterval>|undefined,submissionKey:string|undefined
// A stopped or starting package refuses with its lifecycle code: the panel
// says so and checks again on its own, as ExecutorNotice does.
const lifecycle=ref<AppLifecycle>()
watch([url,dest,name],()=>{submissionKey=undefined},{flush:'sync'})
const pending=(j:Job)=>['queued','running','waiting_executor'].includes(j.state)
const tone={waiting_executor:'info',queued:'info',running:'info',succeeded:'success',failed:'danger',canceled:'neutral',interrupted:'danger'} as const
const waitingForExecutor=computed(()=>jobs.value.some(j=>j.state==='waiting_executor'))
const startupCodes=['EXECUTOR_UNAVAILABLE','EXECUTOR_TIMEOUT']
const waitingError=computed(()=>waitingForExecutor.value&&startupCodes.includes((error.value as {code?:string})?.code??''))
// Until the first answer the lists wait in the rows they will fill, so the page does not move.
const loading=computed(()=>!status.value&&!error.value&&!lifecycle.value)
const showCapability=computed(()=>status.value&&status.value.state!=='ready'&&!(waitingForExecutor.value&&status.value.state==='needs-admin-approval'&&startupCodes.includes(status.value.reason??'')))
async function refresh(){
 if(disposed)return
 const [capability,list,work]=await Promise.allSettled([props.ctx.call<CapabilityStatus>('delegate.status'),props.ctx.call<{delegations:Grant[]}>('delegate.list'),props.ctx.call<{jobs:Job[]}>('delegate.jobs.list')])
 if(disposed)return
 if(capability.status==='fulfilled')status.value=capability.value
 if(list.status==='fulfilled')grants.value=(list.value.delegations??[]).filter(g=>!g.revoked)
 if(work.status==='fulfilled')jobs.value=work.value.jobs??[]
 const refusal=[capability,list,work].find(result=>result.status==='rejected')
 lifecycle.value=refusal&&appLifecycle(refusal.reason)
 if(refusal&&!lifecycle.value)throw refusal.reason
}
async function action(work:()=>Promise<unknown>){if(busy.value||disposed)return;busy.value=true;error.value=undefined;try{await work()}catch(e){if(!disposed)error.value=e}finally{if(!disposed)busy.value=false}}
const reload=()=>action(refresh)
useLifecycleRetry(()=>lifecycle.value,reload)
const revoke=(id:string)=>action(async()=>{await props.ctx.call('delegate.revoke',{delegation_id:id});await refresh()})
const cancel=(id:string)=>action(async()=>{const out=await props.ctx.call<{revocation_pending:boolean}>('delegate.jobs.cancel',{id});if(!disposed)notice.value=out.revocation_pending?copy.value.pending:'';await refresh()})
const canSubmit=computed(()=>status.value?.state==='ready'&&!!dest.value&&!!url.value&&!!name.value)
const submit=()=>action(async()=>{if(!canSubmit.value)return;await props.ctx.call('delegate.jobs.submit',{key:submissionKey??(submissionKey=crypto.randomUUID()),url:url.value,dest:dest.value,name:name.value});if(!disposed){url.value='';name.value='';dest.value=''};await refresh()})
const time=(date:string)=>{const d=new Date(date);return Number.isNaN(d.getTime())?'—':d.toLocaleString(props.ctx.locale)}
defineExpose({refresh:reload})
onMounted(()=>{void reload();timer=setInterval(()=>{if(jobs.value.some(pending))void reload()},3000)})
onBeforeUnmount(()=>{disposed=true;if(timer)clearInterval(timer);provider.dispose()})
</script>
<template>
 <SettingsSection :title="copy.title" :description="copy.help" :collapsible="false" :aria-busy="busy||loading" class="delegate-panel">
  <Callout v-if="error&&!waitingError" tone="danger" role="alert">{{ lifecycleRefusal(error,ctx.locale)??errorCopy(error,copy,ctx.locale,ctx.displayName) }}</Callout>
  <Callout v-if="lifecycle" tone="info" role="status">{{ lifecycleCopy(lifecycle,ctx.locale) }}</Callout>
  <p v-if="notice" role="status">{{ notice }}</p>
  <p v-if="showCapability" role="status">{{ status?.state==='needs-admin-approval'?copy.needs:copy.blocked }}</p>
  <Form direction="vertical" @submit.prevent="submit">
   <FormItem :label="copy.url" label-for="delegate-url"><Input id="delegate-url" v-model="url" type="url" :disabled="busy||status?.state!=='ready'" /></FormItem>
   <FormItem :label="copy.name" label-for="delegate-name"><Input id="delegate-name" v-model="name" data-file-name :disabled="busy||status?.state!=='ready'" /></FormItem>
   <p>{{ copy.size }}</p>
   <p v-if="dest" role="status" class="destination">{{ copy.destination }}{{ separator }}<PathDisplay :path="dest" /></p>
   <FormActions><Button type="button" variant="secondary" :disabled="busy||status?.state!=='ready'" @click="picking=true">{{ copy.choose }}</Button><Button type="submit" variant="primary" :disabled="busy||!canSubmit">{{ copy.submit }}</Button></FormActions>
  </Form>
  <NasPicker v-model:open="picking" :locale="ctx.locale" :name="ctx.displayName" :provider="provider" mode="folder" @selected="dest=$event" />
  <SettingsSection :title="copy.downloads" :collapsible="false" class="delegate-group">
  <p v-if="loading" data-placeholder-row><Skeleton width="12rem" height="20px" /></p>
  <p v-else-if="!jobs.length&&!error">{{ copy.noJobs }}</p>
  <div v-else role="list"><ListItem v-for="job in jobs" :key="job.id" :title="job.name" :data-job-state="job.state">
   <template #meta><Badge :tone="tone[job.state]" :role="job.state==='waiting_executor'?'status':undefined" data-job-badge>{{ copy[job.state] }}</Badge></template>
   <template v-if="pending(job)" #actions><Button variant="secondary" :disabled="busy" @click="cancel(job.id)">{{ copy.cancel }}</Button></template>
  </ListItem></div>
  </SettingsSection>
  <SettingsSection :title="copy.delegations" :collapsible="false" class="delegate-group">
  <p v-if="loading" data-placeholder-row><Skeleton width="12rem" height="20px" /></p>
  <p v-else-if="!grants.length&&!error">{{ copy.noDelegations }}</p>
  <div v-else role="list"><ListItem v-for="grant in grants" :key="grant.delegation_id">
   <template #title><PathDisplay :path="grant.path" /></template>
   <template #description>{{ copy.expires }}{{ separator }}{{ time(grant.expires_at) }}</template>
   <template #actions><Button variant="secondary" :disabled="busy" @click="revoke(grant.delegation_id)">{{ copy.revoke }}</Button></template>
  </ListItem></div>
  </SettingsSection>
 </SettingsSection>
</template>
<style scoped>
.delegate-panel {min-width:0;overflow-wrap:anywhere;}
.delegate-group {margin-top:var(--ui-space-6,24px);}
.destination {min-width:0;max-width:100%;overflow-wrap:anywhere;}
.destination {display:flex;align-items:center;gap:var(--ui-space-2,8px);}
</style>
