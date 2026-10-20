<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref} from 'vue'
import {Badge,Button,Callout,SettingsRow,SettingsSection,Skeleton} from '@synopackageland/ui'
import type {SharedFolderAccess,SharedFolderItem,SharedFolderStatus} from '../context'
import {format,labelSeparator} from '../i18n'
// The Shared Folders the install wizard stored, and what the package account may do in each.
// Loading never blocks the page: the section holds one placeholder row until the backend
// answers, shows each folder's state, and raises a callout only when a folder needs attention.
// An administrator grants the access in one click where the window offers DSM's session
// channel; everyone else, and every failure, gets the Control Panel steps.
const props=defineProps<{shared:SharedFolderAccess;locale?:string}>()
const m=props.shared.messages
const status=ref<SharedFolderStatus>(),failed=ref(false),busy=ref(''),error=ref('')
let disposed=false
const tone={ready:'success',pending:'warning',missing:'danger',ambiguous:'danger'} as const
const text=(folder:SharedFolderItem)=>({account:status.value?.account??'',folder:folder.name,access:m[folder.required]??folder.required})
const fill=(template:string,values:Record<string,string>)=>format(template,values,props.locale)
const attention=computed(()=>status.value?.folders.filter(f=>f.state!=='ready')??[])
const sep=labelSeparator(props.locale??'en')
const canGrant=computed(()=>props.shared.canGrant())
function show(next:SharedFolderStatus){if(!disposed){status.value=next;failed.value=false}}
async function load(){
 try{show(await props.shared.status())}
 catch{if(!disposed&&!status.value)failed.value=true}
}
async function grant(folder:SharedFolderItem){
 if(busy.value)return
 busy.value=folder.key;error.value=''
 try{show(await props.shared.grant(folder.key))}
 catch(e){if(!disposed)error.value=m[(e as {code?:string})?.code||'']||m.shared_folder_unavailable}
 finally{if(!disposed)busy.value=''}
}
const last=props.shared.lastStatus?.()
if(last)status.value=last
onMounted(load)
onBeforeUnmount(()=>{disposed=true})
</script>
<template>
 <SettingsSection v-if="!status&&!failed" :title="m.title" :collapsible="false" aria-busy="true">
  <SettingsRow label="" data-placeholder-row><Skeleton width="16rem" height="20px" /></SettingsRow>
 </SettingsSection>
 <SettingsSection v-else-if="status&&status.folders.length>0" :title="m.title" :collapsible="false" :aria-busy="!!busy">
  <SettingsRow v-for="folder in status.folders" :key="folder.key" :label="folder.name" :data-shared-folder="folder.key">
   <div class="sf-state">
    <Badge :tone="tone[folder.state]" :data-shared-folder-state="folder.state">{{ m[folder.state] }}</Badge>
    <span>{{ m.required }}{{ sep }}{{ m[folder.required] }}</span>
    <span>{{ m.granted }}{{ sep }}{{ m[folder.granted] }}</span>
    <Button v-if="folder.state==='pending'&&canGrant" variant="primary" :disabled="!!busy" @click="grant(folder)">{{ m.grant }}</Button>
   </div>
  </SettingsRow>
  <div class="sf-notices">
  <Callout v-for="folder in attention" :key="folder.key" tone="warning" data-shared-folder-notice>
   <span role="status">
    <template v-if="folder.state==='pending'">{{ m.pendingNote }} {{ canGrant?'':fill(m.askAdmin,text(folder)) }} {{ fill(m.steps,text(folder)) }}</template>
    <template v-else-if="folder.state==='missing'">{{ fill(m.missingHint,text(folder)) }}</template>
    <template v-else>{{ fill(m.ambiguousHint,text(folder)) }}</template>
   </span>
  </Callout>
  <Callout v-if="error" tone="danger"><span role="alert">{{ error }}</span></Callout>
  </div>
  <template v-if="attention.some(f=>f.state==='pending')" #footer>
   <Button variant="secondary" @click="shared.openControlPanel()">{{ m.openControlPanel }}</Button>
  </template>
 </SettingsSection>
</template>
<style scoped>
.sf-state { display:flex; flex-wrap:wrap; align-items:center; gap:var(--ui-space-2,8px) var(--ui-space-3,12px); min-width:0; overflow-wrap:anywhere; }
.sf-notices { display:grid; gap:var(--ui-space-2,8px); min-width:0; max-width:100%; overflow-wrap:anywhere; }
</style>
