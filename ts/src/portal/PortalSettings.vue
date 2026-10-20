<script lang="ts">
// Keep recovery visible if the App remounts settings on the same page.
const pageRemovals=new WeakMap<PortalAccess,'removedFromPage'|'removalUnconfirmed'>()
</script>
<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref} from 'vue'
import {Button,Callout,Dialog,Form,FormActions,Input,Link,SettingsRow,SettingsSection,Skeleton} from '@synopackageland/ui'
import type {PortalAccess,PortalStatus} from '../context'
import {useExecutorSetup} from '../delegate/use-executor-setup'
// SDK text in DSM's language comes with ctx.portal; DSM decides who may change the alias.
// With status.entry (below DSM 7.2) the alias form needs the registered entry.
// Approving the executor, or checking it again, changes what the entry allows.
const props=defineProps<{portal:PortalAccess}>()
const setup=useExecutorSetup()
const m=props.portal.messages
// The section starts as the status the access read last and reads it again
//. reading: with none yet, the first status is on its way; the section
// shows the link, alias and actions rows an administrator sees, so the page
// does not move when it arrives (the regression test F6, the regression test D2).
const status=ref<PortalStatus>(),alias=ref(''),busy=ref(false),error=ref(''),reading=ref(true)
const confirmRemoval=ref(false),recovery=ref(pageRemovals.get(props.portal))
let usesEntry=false
let disposed=false
function show(next:PortalStatus){
 if(disposed)return
 status.value=next;alias.value=next.alias||next.defaultAlias
 // A portal's relative API routes disappear with its alias. Match the actual
 // active URL, not fullPage (which also includes DSM's ?standalone launch).
 if(next.url){
  try{const url=new URL(next.url);usesEntry ||= url.origin===window.location.origin&&window.location.pathname.startsWith(url.pathname)}catch{/* no usable portal URL */}
 }
}
const last=props.portal.lastStatus?.()
if(last)show(last)
// DSM reloads its web server on a change, so the reply can come late or never.
// After REPLY_MS the panel shows a fresh status and stays busy until the change
// ends either way; then it reads the status again.
const REPLY_MS=5000
const late=Symbol('late')
async function run(change:()=>Promise<PortalStatus>){
 if(busy.value)return
 busy.value=true;error.value=''
 const pending=change()
 try{
  const first=await Promise.race([pending,new Promise<typeof late>(r=>setTimeout(()=>r(late),REPLY_MS))])
  if(first!==late){show(first);return}
  show(await props.portal.status())
  try{await pending}
  finally{show(await props.portal.status())}
 }
 catch(e){if(!disposed)error.value=m[(e as {code?:string})?.code||'']||m.portal_unavailable}
 finally{if(!disposed)busy.value=false}
}
// The entry gates the alias form; the note says what the panel needs next.
const entryPending=computed(()=>!!status.value?.canManage&&!!status.value.entry&&status.value.entry!=='registered')
const note=computed(()=>{
 const s=status.value
 if(!s)return undefined
 if(entryPending.value)return s.entry==='unregistered'?m.entryOff:m.cleanupRequired
 if(s.state==='active')return undefined
 if(s.canManage)return m.inactive
 return s.controlPanel?undefined:m.askAdmin
})
// link ends in a colon for the CLI's plain panel; the row's label column separates it.
const linkLabel=m.link?.replace(/\s*[:：]\s*$/,'')
// Enabling the entry also applies the alias: an entry without one stays inactive.
// A refused alias leaves the registered entry and the form to choose another.
async function enable(){
 const next=await props.portal.register!()
 if(!(next.canManage&&next.entry==='registered'&&next.state==='inactive'))return next
 show(next)
 return props.portal.apply(next.alias||next.defaultAlias)
}
function load(){if(!busy.value&&!recovery.value)void props.portal.status().then(show,()=>{}).finally(()=>{if(!disposed)reading.value=false})}
function remove(){if(usesEntry)confirmRemoval.value=true;else void run(()=>props.portal.unregister!())}
async function removeFromPage(){
 confirmRemoval.value=false
 if(busy.value)return
 busy.value=true
 // The web server can drop the reply as well as the next status request.
 // Give a useful next step immediately, without claiming removal succeeded.
 recovery.value='removalUnconfirmed';pageRemovals.set(props.portal,recovery.value)
 try{await props.portal.unregister!();recovery.value='removedFromPage';pageRemovals.set(props.portal,recovery.value)}
 catch{/* The DSM desktop is the independent route for checking the result. */}
 finally{if(!disposed)busy.value=false}
}
onMounted(()=>{setup.subscribe?.(load);load()})
onBeforeUnmount(()=>{disposed=true})
</script>
<template>
 <SettingsSection v-if="recovery" :title="m.title" :collapsible="false">
  <Callout tone="info"><span role="status">{{ m[recovery] }}</span></Callout>
  <Link href="/" external>{{ m.openDesktop }}</Link>
 </SettingsSection>
 <SettingsSection v-else-if="status||reading" :title="m.title" :description="note" :collapsible="false" :aria-busy="busy||!status">
  <template v-if="!status"><SettingsRow v-for="([label,height],i) in [[linkLabel,'20px'],[m.alias,'32px'],['','32px']]" :key="i" :label="label" data-placeholder-row><Skeleton width="16rem" :height="height" /></SettingsRow></template>
  <template v-else>
  <SettingsRow v-if="status.state==='active'" :label="linkLabel"><Link :href="status.url" external>{{ status.url }}</Link></SettingsRow>
  <Form v-if="status.canManage&&!entryPending" @submit.prevent="run(()=>portal.apply(alias))">
   <SettingsRow :label="m.alias"><Input id="portal-alias" v-model="alias" :aria-label="m.alias" maxlength="64" autocomplete="off" :disabled="busy" /></SettingsRow>
   <SettingsRow label="" field-width="full">
    <FormActions>
     <Button type="submit" variant="primary" :disabled="busy">{{ m.apply }}</Button>
     <Button v-if="status.state==='active'" variant="secondary" :disabled="busy" @click="run(()=>portal.disable())">{{ m.disable }}</Button>
     <Button variant="secondary" @click="portal.openControlPanel()">{{ m.advanced }}</Button>
     <Button v-if="status.entry" variant="secondary" :disabled="busy" @click="remove">{{ m.unregister }}</Button>
    </FormActions>
   </SettingsRow>
  </Form>
  <Callout v-if="error" tone="danger"><span role="alert">{{ error }}</span></Callout>
  </template>
  <template v-if="status&&entryPending" #footer>
   <Button v-if="status.entry==='unregistered'" variant="primary" :disabled="busy" @click="run(enable)">{{ m.register }}</Button>
   <Button v-else variant="primary" :disabled="busy" @click="remove">{{ m.unregister }}</Button>
  </template>
  <template v-else-if="status&&!status.canManage&&status.controlPanel" #footer>
   <Button variant="secondary" @click="portal.openControlPanel()">{{ m.controlPanel }}</Button>
  </template>
 </SettingsSection>
 <Dialog v-model:open="confirmRemoval" :title="m.unregister" :description="m.removeFromPage" :close-label="m.cancel">
  <template #footer><Button variant="secondary" @click="confirmRemoval=false">{{ m.cancel }}</Button><Button variant="danger" :disabled="busy" @click="removeFromPage">{{ m.unregister }}</Button></template>
 </Dialog>
</template>
