<script setup lang="ts">
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {Button,Callout,Dialog} from '@synopackageland/ui'
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
 <Dialog :open="open" :title="title??(mode==='folder'?copy.choose:copy.chooseFile)" :description="description??copy.ownPermissions" :close-label="copy.close" :show-close="!busy" width="800px" @escape-key-down="event=>{if(busy)event.preventDefault()}" @interact-outside="event=>{if(busy)event.preventDefault()}" @update:open="value=>{if(!value)close()}">
  <p v-if="destination" class="save-consent">{{ save.selection }}</p>
  <Callout v-if="error" tone="danger" role="alert" class="save-message">{{ errorText?.(error)??saveError(error,save,'select',locale,name) }}</Callout>
  <div class="nas-picker-content" :aria-busy="busy" :inert="busy"><FileBrowser v-if="open" :provider="provider" :locale="locale" :name="name" :mode="mode" @selected="selected=$event" /></div>
  <template #footer><Button variant="secondary" :disabled="busy" @click="close">{{ copy.cancel }}</Button><Button variant="primary" :disabled="!selected||busy||!!destination&&mode!=='folder'" @click="confirm">{{ copy.confirm }}</Button></template>
 </Dialog>
</template>
<style scoped>
.nas-picker-content { max-height: 65vh; overflow: auto; }
.save-message {--ui-text-secondary:var(--ui-text);}
.save-consent {color:var(--ui-text);margin-block:0 var(--ui-space-4,16px);}
</style>
