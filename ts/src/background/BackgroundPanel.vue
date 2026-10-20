<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref} from 'vue'
import {Badge,Button,Callout,Card,Checkbox,Form,FormActions,FormItem,Link,PathDisplay,Select,SettingsSection,Skeleton} from '@synopackageland/ui'
import type {Context} from '../context'
import NasPicker from '../nas/NasPicker.vue'
import {format,type Message} from '../i18n'
import {formatSize} from '../files/size'
import {backgroundCopy,backgroundErrorCopy} from './i18n'
import {createBackgroundPickerProvider} from './picker-provider'
import {requestKey} from './request-key'
import {appLifecycle,lifecycleCopy,type AppLifecycle} from '../delegate/lifecycle'
import {useLifecycleRetry} from '../delegate/use-lifecycle-retry'
interface Status{owner?:Owner;admin:boolean;revision:number;worker:{username:string;alive:boolean}|null;limits:{maxInputBytes:number;maxQueued:number;maxOwnerPending:number;maxOwnerBytes:number;jobTimeoutSeconds:number}}
interface Owner{user:string;uid:string}
interface Progress{done:number;total:number;bytes:number;totalBytes:number;item?:string}
interface Job{copyRetained?:boolean;scheduleId?:string;submitter:Owner;id:string;name:string;state:'queued'|'running'|'paused'|'succeeded'|'failed'|'canceled'|'interrupted';pauseRequested?:boolean;delegation_id?:string;progress?:Progress}
interface Schedule{id:string;name:string;owner:Owner;enabled:boolean;nextAt:string;intervalSeconds:number;workerRevision:number;missed:number;reason?:string}
const props=defineProps<{ctx:Pick<Context,'call'|'locale'|'displayName'>}>()
const copy=computed(()=>backgroundCopy(props.ctx.locale))
const fill=(text:Message,values:Record<string,string|number>)=>format(text,values,props.ctx.locale)
const status=ref<Status>(),jobs=ref<Job[]>([]),error=ref<unknown>(),unsupported=ref(false),busy=ref(false)
const schedules=ref<Schedule[]>([]),scheduleSource=ref<Job>(),scheduleConsent=ref(false),scheduleInterval=ref('3600'),scheduleDelete=ref<Schedule>(),scheduleEnable=ref<Schedule>()
const purgeOwner=ref<Owner>(),refreshing=ref(false)
// A stopped or starting package refuses a refresh: the panel says so and
// refreshes again on its own. An action it refuses stays an error.
const lifecycle=ref<AppLifecycle>()
// Until the first status the panel shows its status lines waiting and its buttons off, so the page does not move.
const loading=computed(()=>!status.value&&!error.value&&!unsupported.value&&!lifecycle.value)
const picking=ref(false),picked=ref<{folder:string;relative:string}>(),download=ref(''),downloadName=ref('')
const picker=createBackgroundPickerProvider(props.ctx)
let timer:ReturnType<typeof setInterval>|undefined,disposed=false
const settled=(j:Job)=>!['queued','running'].includes(j.state)
// A paused job waits for a resume: it is not polled, but it can still be canceled.
const finished=(j:Job)=>settled(j)&&j.state!=='paused'
const pausing=(j:Job)=>j.state==='running'&&j.pauseRequested
const stateText=(j:Job)=>pausing(j)?copy.value.pausing:copy.value[`state_${j.state}`]
const stateTone=(j:Job)=>pausing(j)?'warning':({queued:'info',running:'info',paused:'warning',succeeded:'success',failed:'danger',canceled:'neutral',interrupted:'danger'} as const)[j.state]
async function call<T>(method:string,params?:Record<string,unknown>,read=false):Promise<T|undefined>{
 try{return await props.ctx.call<T>(method,params)}
 catch(e){
  if(disposed)return undefined
  if(read&&(lifecycle.value=appLifecycle(e)))return undefined
  if((e as {code?:string})?.code==='unsupported'){unsupported.value=true;jobs.value=[];schedules.value=[];scheduleSource.value=undefined;scheduleDelete.value=undefined;scheduleEnable.value=undefined;status.value=undefined;purgeOwner.value=undefined;picked.value=undefined;if(download.value)URL.revokeObjectURL(download.value);download.value=''}
  else if(!disposed)error.value=e
  return undefined
 }
}
async function refresh(){
 if(disposed||refreshing.value)return
 refreshing.value=true
 try{
 const s=await call<Status>('bg.status',undefined,true)
 if(!s||disposed)return
 lifecycle.value=undefined
 if(!s.limits||typeof s.admin!=='boolean'){error.value={code:'unavailable'};return}
 unsupported.value=false;status.value=s
 const list=await call<{jobs:Job[]}>('bg.jobs.list',undefined,true)
 if(list&&!disposed)jobs.value=list.jobs??[]
 const scheduleList=await call<{schedules:Schedule[]}>('bg.schedules.list',undefined,true)
 if(scheduleList&&!disposed)schedules.value=scheduleList.schedules??[]
 if(!disposed&&jobs.value.every(settled)&&schedules.value.every(s=>!s.enabled)&&timer){clearInterval(timer);timer=undefined}
 }finally{refreshing.value=false}
}
function poll(){if(!timer)timer=setInterval(()=>void refresh(),3000)}
async function guarded(work:()=>Promise<unknown>){
 if(busy.value)return
 busy.value=true;error.value=undefined
 try{await work();await refresh();if(schedules.value.some(s=>s.enabled)||jobs.value.some(j=>!settled(j)))poll()}finally{if(!disposed)busy.value=false}
}
const purge=()=>guarded(async()=>{if(!purgeOwner.value)return;const out=await call('bg.jobs.purge',{owner:purgeOwner.value});if(out)purgeOwner.value=undefined})
const intervalOptions=computed(()=>[{value:'3600',label:copy.value.hourly},{value:'86400',label:copy.value.daily},{value:'604800',label:copy.value.weekly}])
const selectSchedule=(job:Job)=>{scheduleSource.value=job;scheduleConsent.value=false;scheduleInterval.value='3600'}
const createSchedule=()=>guarded(async()=>{if(!scheduleSource.value||!scheduleConsent.value)return;const out=await call('bg.schedules.create',{key:requestKey(),jobId:scheduleSource.value.id,intervalSeconds:Number(scheduleInterval.value),consent:true});if(out)scheduleSource.value=undefined})
const disableSchedule=(id:string)=>guarded(()=>call('bg.schedules.disable',{id}))
const deleteSchedule=()=>guarded(async()=>{if(!scheduleDelete.value)return;const out=await call('bg.schedules.delete',{id:scheduleDelete.value.id});if(out)scheduleDelete.value=undefined})
const enableSchedule=()=>guarded(async()=>{if(!scheduleEnable.value)return;const out=await call('bg.schedules.enable',{id:scheduleEnable.value.id,consent:true});if(out)scheduleEnable.value=undefined})
const formatTime=(value:string)=>{const date=new Date(value);return Number.isFinite(date.getTime())?date.toLocaleString():copy.value.unknownTime}
const ownCopy=(job:Job)=>job.copyRetained&&job.state==='succeeded'&&!job.scheduleId&&job.submitter?.user===status.value?.owner?.user&&job.submitter?.uid===status.value?.owner?.uid
const cancel=(id:string)=>guarded(()=>call('bg.jobs.cancel',{id}))
const pause=(id:string)=>guarded(()=>call('bg.jobs.pause',{id}))
const resume=(id:string)=>guarded(()=>call('bg.jobs.resume',{id}))
const pick=(path:string)=>{const at=path.lastIndexOf('/');picked.value={folder:path.slice(0,at),relative:path.slice(at+1)}}
// The signed-in caller's own File Station session decides whether the
// file can be read; the submit itself is the consent to copy that one file.
const submit=()=>guarded(async()=>{
 if(!picked.value)return
 const sent=await call('bg.jobs.submit',{key:requestKey(),...picked.value})
 if(sent)picked.value=undefined
})
async function fetchResult(job:Job){
 const value=await call<{result:unknown}>('bg.jobs.result',{id:job.id})
 if(!value||disposed)return
 if(download.value)URL.revokeObjectURL(download.value)
 download.value=URL.createObjectURL(new Blob([JSON.stringify(value.result,null,2)],{type:'application/json'}))
 downloadName.value=`${job.name||job.id}.sha256.json`
}
useLifecycleRetry(()=>lifecycle.value,refresh)
defineExpose({refresh})
onMounted(()=>void refresh().then(()=>{if(schedules.value.some(s=>s.enabled)||jobs.value.some(j=>!settled(j)))poll()}))
onBeforeUnmount(()=>{disposed=true;picker.dispose();if(timer)clearInterval(timer);if(download.value)URL.revokeObjectURL(download.value)})
</script>
<template>
 <SettingsSection :aria-busy="busy||refreshing||loading" :title="copy.title" :description="unsupported?undefined:copy.help" :collapsible="false" data-background-panel>
  <Callout v-if="unsupported" tone="warning">{{ copy.unsupported }}</Callout>
  <template v-else>
   <Callout v-if="lifecycle" tone="info" role="status">{{ lifecycleCopy(lifecycle,ctx.locale) }}</Callout>
   <Callout v-if="error" tone="danger" role="alert">{{ backgroundErrorCopy(error,copy,ctx.locale,ctx.displayName) }}</Callout>
   <template v-if="loading">
    <p v-for="(width,i) in ['14rem','24rem','18rem','24rem']" :key="i" data-placeholder-row><Skeleton :width="width" height="20px" /></p>
    <div class="integration-actions">
      <Button variant="secondary" disabled>{{ copy.chooseFile }}</Button>
      <Button variant="primary" disabled>{{ copy.submit }}</Button>
    </div>
   </template>
   <template v-if="status">
    <p role="status">{{ status.worker?.alive ? fill(copy.workerRunning,{user:status.worker.username}) : copy.workerStopped }}</p>
    <p class="integration-help">{{ fill(copy.limits,{size:status.limits.maxInputBytes/1024,queue:status.limits.maxQueued,seconds:status.limits.jobTimeoutSeconds}) }}</p>
    <p class="integration-help">{{ status.admin ? copy.adminScope : copy.ownerScope }}</p>
    <p class="integration-help">{{ fill(copy.ownerLimits,{jobs:status.limits.maxOwnerPending,size:status.limits.maxOwnerBytes/1024}) }}</p>
    <div class="integration-actions">
      <Button variant="secondary" :disabled="busy" @click="picking=true">{{ copy.chooseFile }}</Button>
      <Button variant="primary" :disabled="busy||!picked" @click="submit">{{ copy.submit }}</Button>
    </div>
    <p v-if="picked" role="status" data-bg-picked class="bg-path">{{ fill(copy.chosen,{path:''}).trim() }} <PathDisplay :path="`${picked.folder}/${picked.relative}`" /></p>
    <NasPicker v-model:open="picking" :locale="ctx.locale" :name="ctx.displayName" :provider="picker" mode="file" :title="copy.pickerTitle" :description="copy.pickerConsent" @selected="pick" />
    <Callout v-if="purgeOwner&&status.admin" tone="warning">
     <p>{{ fill(copy.purgeConfirm,{user:purgeOwner.user,uid:purgeOwner.uid}) }}</p>
     <div class="integration-actions">
       <Button variant="secondary" :disabled="busy" @click="purge">{{ copy.confirmPurge }}</Button>
       <Button variant="secondary" :disabled="busy" @click="purgeOwner=undefined">{{ copy.keepJobs }}</Button>
     </div>
    </Callout>
    <p v-if="!jobs.length">{{ copy.noJobs }}</p>
    <div v-else class="bg-cards">
     <Card v-for="job in jobs" :key="job.id" :title="job.name" layout-type="compact" :data-job-state="job.state" :data-job-id="job.id">
      <template #header-actions><Badge :tone="stateTone(job)" data-bg-state>{{ stateText(job) }}</Badge></template>
      <div class="bg-card-body">
       <span v-if="job.progress?.total" class="integration-help">{{ fill(copy.progressItems,{done:job.progress.done,count:job.progress.total}) }}</span>
       <span v-if="job.progress?.totalBytes" class="integration-help">{{ fill(copy.progressBytes,{done:formatSize(job.progress.bytes,ctx.locale),total:formatSize(job.progress.totalBytes,ctx.locale)}) }}</span>
       <span v-if="job.progress?.item" class="integration-help bg-path">{{ fill(copy.progressItem,{item:''}).trim() }} <PathDisplay :path="job.progress.item" /></span>
       <span v-if="status.admin&&job.submitter" class="integration-help">{{ fill(copy.owner,{user:job.submitter.user,uid:job.submitter.uid}) }}</span>
       <span v-if="job.scheduleId" class="integration-help">{{ copy.scheduledJob }}</span>
       <div class="integration-actions">
        <Button v-if="status.admin&&job.submitter" variant="secondary" :disabled="busy" @click="purgeOwner=job.submitter">{{ copy.purgeOwner }}</Button>
        <Button v-if="!settled(job)&&!job.pauseRequested&&!job.delegation_id" variant="secondary" :disabled="busy" @click="pause(job.id)">{{ copy.pause }}</Button>
        <Button v-if="job.state==='paused'||pausing(job)" variant="secondary" :disabled="busy" @click="resume(job.id)">{{ copy.resume }}</Button>
        <Button v-if="!finished(job)" variant="secondary" :disabled="busy" @click="cancel(job.id)">{{ copy.cancel }}</Button>
        <Button v-if="ownCopy(job)" variant="secondary" :disabled="busy" @click="selectSchedule(job)">{{ copy.scheduleCopy }}</Button>
        <Button v-if="job.state==='succeeded'" variant="secondary" :disabled="busy" @click="fetchResult(job)">{{ copy.result }}</Button>
       </div>
      </div>
     </Card>
    </div>
    <SettingsSection data-bg-schedules class="bg-schedules" :title="copy.schedulesTitle" :description="copy.scheduleHelp" :collapsible="false">
     <Form v-if="scheduleSource" class="bg-schedule-form" @submit.prevent="createSchedule">
      <p>{{ fill(copy.scheduleSource,{name:scheduleSource.name}) }}</p>
      <FormItem :label="copy.interval"><Select v-model="scheduleInterval" data-schedule-interval :options="intervalOptions" :disabled="busy" /></FormItem>
      <Checkbox v-model="scheduleConsent" data-schedule-consent :disabled="busy" :label="copy.scheduleConsent" />
      <FormActions><Button type="submit" variant="primary" :disabled="busy||!scheduleConsent">{{ copy.createSchedule }}</Button><Button type="button" variant="secondary" :disabled="busy" @click="scheduleSource=undefined">{{ copy.cancel }}</Button></FormActions>
     </Form>
     <p v-if="!schedules.length">{{ copy.noSchedules }}</p>
     <div v-else class="bg-cards">
      <Card v-for="schedule in schedules" :key="schedule.id" :title="schedule.name" layout-type="compact" :data-schedule-id="schedule.id">
       <template #header-actions><Badge :tone="schedule.enabled?'success':'neutral'" data-bg-state>{{ schedule.enabled ? copy.scheduleOn : copy.scheduleOff }}</Badge></template>
       <div class="bg-card-body">
        <span v-if="status.admin" class="integration-help">{{ fill(copy.owner,{user:schedule.owner.user,uid:schedule.owner.uid}) }}</span>
        <span v-if="schedule.enabled" class="integration-help">{{ fill(copy.nextRun,{time:formatTime(schedule.nextAt)}) }}</span>
        <span class="integration-help">{{ fill(copy.missedRuns,{count:schedule.missed}) }}</span>
        <Callout v-if="schedule.reason==='worker-changed'" tone="warning">{{ copy.revisionChanged }}</Callout>
        <Callout v-else-if="schedule.reason==='input-unavailable'" tone="warning">{{ copy.errors['input-unavailable'] }}</Callout>
        <div class="integration-actions">
         <Button v-if="schedule.enabled" variant="secondary" :disabled="busy" @click="disableSchedule(schedule.id)">{{ copy.disableSchedule }}</Button>
         <Button v-else-if="status.admin" variant="secondary" :disabled="busy" @click="scheduleEnable=schedule">{{ copy.enableSchedule }}</Button>
         <Button variant="secondary" :disabled="busy" @click="scheduleDelete=schedule">{{ copy.deleteSchedule }}</Button>
        </div>
       </div>
      </Card>
     </div>
     <Callout v-if="scheduleDelete" tone="warning">
      <p>{{ fill(copy.deleteScheduleConsent,{name:scheduleDelete.name}) }}</p>
      <div class="integration-actions"><Button variant="danger" :disabled="busy" @click="deleteSchedule">{{ copy.confirmDeleteSchedule }}</Button><Button variant="secondary" :disabled="busy" @click="scheduleDelete=undefined">{{ copy.cancel }}</Button></div>
     </Callout>
     <Callout v-if="scheduleEnable" tone="warning">
      <p>{{ fill(copy.enableScheduleConsent,{name:scheduleEnable.name}) }}</p>
      <div class="integration-actions"><Button variant="primary" :disabled="busy" @click="enableSchedule">{{ copy.confirmEnableSchedule }}</Button><Button variant="secondary" :disabled="busy" @click="scheduleEnable=undefined">{{ copy.cancel }}</Button></div>
     </Callout>
    </SettingsSection>
    <Link v-if="download" :href="download" :download="downloadName">{{ copy.download }}</Link>
   </template>
  </template>
 </SettingsSection>
</template>

<style scoped>
.bg-cards { display:grid; gap:var(--ui-space-3,12px); min-width:0; overflow-wrap:anywhere; }
.bg-card-body { display:flex; flex-direction:column; align-items:flex-start; gap:var(--ui-space-2,8px); min-width:0; }
.bg-card-body > span { min-width:0; max-width:100%; }
.bg-card-body > .integration-actions { margin-block:0; }
.integration-actions { display:flex; flex-wrap:wrap; gap:var(--ui-space-2,8px); margin-block:var(--ui-space-3,12px); }
.bg-schedules { margin-block-start: var(--ui-space-6,24px); min-width:0; overflow-wrap:anywhere; }
.bg-schedules p { min-width:0; max-width:100%; }
.bg-schedule-form { margin-block:var(--ui-space-4,16px); }
.bg-path { display:flex; align-items:center; gap:var(--ui-space-2,8px); min-width:0; max-width:100%; }
</style>
