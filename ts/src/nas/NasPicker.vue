<script setup lang="ts">
import {computed,onBeforeUnmount,ref,useId,watch} from 'vue'
import {Button,Callout,Dialog,InfoTip} from '@synopackageland/ui'
import {format} from '../i18n'
import FileBrowser from '../files/FileBrowser.vue'
import type {NasProvider} from './contract'
import {nasCopy} from './locale'
import type {DestinationGrant,DestinationSelection} from '../delegate/long-running'
import {saveCopy,saveError} from '../delegate/save-copy'
const props=defineProps<{open:boolean;locale:string;name?:string;provider:NasProvider;mode:'folder'|'file';title?:string;description?:string;destination?:DestinationSelection
 // The App's own text for a refused folder choice; undefined keeps the SDK's.
 errorText?:(error:unknown)=>string|undefined}>()
const emit=defineEmits<{'update:open':[value:boolean];selected:[path:string];authorized:[grant:DestinationGrant];refused:[error:unknown]}>()
const selected=ref<string>(),copy=computed(()=>nasCopy(props.locale)),save=computed(()=>saveCopy(props.locale)),busy=ref(false),error=ref<unknown>()
// The lasting grant Confirm makes, read with the button and shown as an info tip.
const consentId=`nas-picker-consent-${useId()}`
let generation=0,disposed=false
watch(()=>props.open,()=>{generation++;selected.value=undefined;error.value=undefined})
onBeforeUnmount(()=>{disposed=true;generation++})
function close(){if(busy.value)return;selected.value=undefined;emit('update:open',false)}
async function confirm(){
 const path=selected.value,destination=props.destination,version=generation
 if(!path||busy.value||!props.open||disposed||(destination&&props.mode!=='folder'))return
 if(!destination){emit('selected',path);close();return}
 busy.value=true;error.value=undefined
 try{
  const grant=await destination.selectDestination({key:destination.key,path})
  if(disposed||version!==generation)return
  if(!grant||typeof grant.id!=='string'||!grant.id||typeof grant.username!=='string'||!grant.username||grant.path!==path||grant.key!==destination.key||grant.lifetime!=='until-revoked')throw {code:'commit-uncertain'}
  emit('authorized',grant);busy.value=false;close()
 }catch(e){
  if(disposed||version!==generation)return
  // Application Privileges refuse this address, not the folder: another
  // choice cannot help, so the picker closes and its owner reports it.
  if((e as {code?:unknown})?.code==='APP_DENIED'){busy.value=false;emit('refused',e);close();return}
  error.value=e
 }
 finally{if(!disposed)busy.value=false}
}
</script>
<template>
 <Dialog :open="open" :title="title??(mode==='folder'?copy.choose:copy.chooseFile)" :description="description" :close-label="copy.close" :show-close="!busy" width="800px" height="600px" @escape-key-down="event=>{if(busy)event.preventDefault()}" @interact-outside="event=>{if(busy)event.preventDefault()}" @update:open="value=>{if(!value)close()}">
  <div class="nas-picker-body">
   <Callout v-if="error" tone="danger" role="alert" class="save-message">{{ errorText?.(error)??saveError(error,save,'select',locale,name) }}</Callout>
   <div class="nas-picker-content" :aria-busy="busy" :inert="busy"><FileBrowser v-if="open" fill :provider="provider" :locale="locale" :name="name" :mode="mode" @selected="selected=$event" /></div>
  </div>
  <template #footer>
   <div class="nas-picker-status">
    <span v-if="selected" class="nas-picker-selection" role="status" :title="selected">{{ format(copy.selected,{name:selected}) }}</span>
    <template v-if="destination"><InfoTip icon="info" size="small" side="top" :content="save.selection" :aria-label="save.selection" /><span :id="consentId" class="nas-picker-consent" hidden>{{ save.selection }}</span></template>
   </div>
   <Button variant="secondary" :disabled="busy" @click="close">{{ copy.cancel }}</Button><Button variant="primary" :aria-describedby="destination?consentId:undefined" :disabled="!selected||busy||!!destination&&mode!=='folder'" @click="confirm">{{ copy.confirm }}</Button>
  </template>
 </Dialog>
</template>
<style scoped>
/* The dialog keeps one size; the browser takes the height the error leaves. */
.nas-picker-body { display: flex; flex-direction: column; gap: var(--ui-space-3, 12px); height: 100%; min-height: 0; }
.nas-picker-body > .save-message { flex: none; }
.nas-picker-content { flex: 1 1 0; min-width: 0; min-height: 0; }
.save-message {--ui-text-secondary:var(--ui-text);}
.nas-picker-status { display: flex; flex: 1 1 0; align-items: center; gap: var(--ui-space-1, 4px); min-width: 0; margin-inline-end: auto; color: var(--ui-text-secondary); }
.nas-picker-consent { display: none !important; }
.nas-picker-selection { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
