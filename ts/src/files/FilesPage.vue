<script setup lang="ts">
import {onBeforeUnmount,onMounted,ref} from 'vue'
import {Button,Callout,PathDisplay,SettingsSection} from '@synopackageland/ui'
import type {Context} from '../context'
import FileBrowser from './FileBrowser.vue'
import NasPicker from '../nas/NasPicker.vue'
import {createNasProvider} from '../nas/provider'
import {nasCopy,nasErrorCopy} from '../nas/locale'
import {appLifecycle,lifecycleCopy,type AppLifecycle} from '../delegate/lifecycle'
import {useLifecycleRetry} from '../delegate/use-lifecycle-retry'
const props=defineProps<{ctx:Context}>()
// A stopped or starting package refuses the session check: the page says so
// and checks again on its own.
const error=ref<unknown>(),lifecycle=ref<AppLifecycle>()
const ready=ref(false),mode=ref<'folder'|'file'>('folder'),open=ref(false),selected=ref(''),generation=ref(0)
const provider=createNasProvider(props.ctx)
let disposed=false,unsubscribe:(()=>void)|undefined
async function check(){
 if(disposed)return
 error.value=undefined
 ready.value=false;selected.value='';open.value=false;generation.value++
 try{await props.ctx.call('nas.session.status');if(!disposed){lifecycle.value=undefined;ready.value=true}}
 catch(e){if(disposed)return;lifecycle.value=appLifecycle(e);if(!lifecycle.value&&(e as {code?:string})?.code!=='unknown_method')error.value=e}
}
useLifecycleRetry(()=>lifecycle.value,check)
onMounted(()=>{unsubscribe=props.ctx.reopen(()=>void check());void check()})
onBeforeUnmount(()=>{disposed=true;unsubscribe?.();provider.dispose()})
</script>
<template>
 <Callout v-if="lifecycle" tone="info" role="status">{{ lifecycleCopy(lifecycle,ctx.locale) }}</Callout>
 <Callout v-if="error" tone="danger"><span role="alert">{{ nasErrorCopy(error,nasCopy(ctx.locale),ctx.locale,ctx.displayName) }}</span><Button variant="secondary" @click="check">{{ nasCopy(ctx.locale).refresh }}</Button></Callout>
 <SettingsSection v-if="ready" :title="nasCopy(ctx.locale).title" :description="nasCopy(ctx.locale).ownPermissions" :collapsible="false">
  <div class="files-actions"><Button variant="secondary" @click="mode='folder';open=true">{{ nasCopy(ctx.locale).choose }}</Button><Button variant="secondary" @click="mode='file';open=true">{{ nasCopy(ctx.locale).chooseFile }}</Button></div>
  <p v-if="selected" class="files-path" role="status"><PathDisplay :path="selected" /></p>
  <FileBrowser :key="generation" :provider="provider" :locale="ctx.locale" :name="ctx.displayName" transfers />
  <NasPicker :provider="provider" :locale="ctx.locale" :name="ctx.displayName" :mode="mode" v-model:open="open" @selected="selected=$event" />
 </SettingsSection>
</template>
<style scoped>
.files-actions { display: flex; flex-wrap: wrap; gap: var(--ui-space-2, 8px); }
.files-path { display: flex; min-width: 0; }
</style>
