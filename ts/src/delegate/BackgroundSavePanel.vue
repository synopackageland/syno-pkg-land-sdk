<script setup lang="ts">
// BackgroundSaveSettings with an opt-in sample producer for template Apps.
import {computed,ref} from 'vue'
import {Button,Callout} from '@synopackageland/ui'
import type {Context} from '../context'
import type {BackgroundSaveFacade,DestinationGrant,DestinationState} from './long-running'
import {saveCopy,saveError} from './save-copy'
import {labelSeparator} from '../i18n'
import BackgroundSaveSettings from './BackgroundSaveSettings.vue'
import type {NasProvider} from '../nas/contract'
const props=defineProps<{sdk:BackgroundSaveFacade;admin:boolean;locale:string
 // Replaces the section title; an empty string drops it where the page header already names the panel.
 title?:string
 picker?:{provider:NasProvider;keys:string[]
 // A folder row's label when an App has several keys; a key itself is never shown.
 labels?:Record<string,string>}
 ctx?:Pick<Context,'call'|'displayName'>
 // The App's own text for a refused folder choice; undefined keeps the SDK's.
 pickerErrorText?:(error:unknown)=>string|undefined
 // Shows "Save a sample file", a demonstration for templates and sample Apps.
 sampleSave?:boolean}>()
const emit=defineEmits<{authorized:[grant:DestinationGrant]}>()
const copy=computed(()=>saveCopy(props.locale)),separator=computed(()=>labelSeparator(props.locale))
const active=ref<DestinationState[]>([])
const settings=ref<InstanceType<typeof BackgroundSaveSettings>>()
const refresh=()=>{error.value=undefined;return settings.value?.refresh()??Promise.resolve()}
const busy=ref(false),error=ref<unknown>()
async function example(key:string){
 if(busy.value)return
 busy.value=true;error.value=undefined
 const job_id='example-'+Array.from(crypto.getRandomValues(new Uint8Array(16)),b=>b.toString(16).padStart(2,'0')).join('')
 try{await props.sdk.exportExample({key,job_id})}catch(e){error.value=e}finally{busy.value=false}
}
const exampleLabel=(key:string)=>props.picker?.labels?.[key]?`${copy.value.example}${separator.value}${props.picker.labels[key]}`:copy.value.example
// The page header's refresh reloads every panel on the page.
defineExpose({refresh})
</script>

<template>
 <div class="background-save-panel">
  <BackgroundSaveSettings ref="settings" :sdk="sdk" :admin="admin" :locale="locale" :title="title" :picker="picker" :ctx="ctx" :picker-error-text="pickerErrorText" @authorized="emit('authorized',$event)" @destinations="active=$event.filter(d=>d.status==='active')">
   <Callout v-if="error" tone="danger" role="alert">{{ saveError(error,copy,'save',locale,ctx?.displayName) }}</Callout>
   <div v-if="sampleSave&&active.length" class="save-example">
    <p class="save-help">{{ copy.exampleHelp }}</p>
    <div class="save-actions"><Button v-for="d in active" :key="d.key" variant="secondary" :disabled="busy" @click="example(d.key)">{{ exampleLabel(d.key) }}</Button></div>
   </div>
  </BackgroundSaveSettings>
 </div>
</template>
<style scoped>
.background-save-panel {min-width:0;overflow-wrap:anywhere;}
.save-help {margin:0 0 var(--ui-space-3,12px);max-width:72ch;color:var(--ui-text-secondary);}
.save-actions {display:flex;align-items:center;flex-wrap:wrap;gap:var(--ui-space-2,8px);margin-bottom:var(--ui-space-3,12px);}
</style>
