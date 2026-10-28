<script setup lang="ts">
// One line above an App's pages once the installed package has replaced the
// UI this page loaded. DSM keeps an App's scripts after a package upgrade
// until the page reloads, so a window opened again still runs the old ones.
// It holds nothing back: the App keeps working until the user reloads.
// ctx.uiUpdate (the CLI's DSM window) answers; without it nothing shows.
// Checks on mount, on reopen and when the page is shown again, at most once
// a minute for the latter; a known stale page checks no more.
import {computed,onBeforeUnmount,onMounted,ref} from 'vue'
import {Button,Callout} from '@synopackageland/ui'
import type {Context} from '../context'
import {shellCopy} from './i18n'
const props=defineProps<{locale?:string;ctx:Partial<Pick<Context,'uiUpdate'|'reopen'>>}>()
const copy=computed(()=>shellCopy(props.locale??'en'))
const stale=ref(false)
const controller=new AbortController()
let stopReopen:(()=>void)|undefined,last=-Infinity
async function check(){
 const update=props.ctx.uiUpdate
 if(!update||stale.value||controller.signal.aborted)return
 last=Date.now()
 try{if(await update.check(controller.signal)&&!controller.signal.aborted)stale.value=true}catch{}
}
function shown(){if(document.visibilityState!=='hidden'&&Date.now()-last>=60_000)void check()}
onMounted(()=>{
 if(!props.ctx.uiUpdate)return
 stopReopen=props.ctx.reopen?.(()=>{void check()})
 document.addEventListener('visibilitychange',shown)
 void check()
})
onBeforeUnmount(()=>{controller.abort();stopReopen?.();document.removeEventListener('visibilitychange',shown)})
function reload(){window.location.reload()}
</script>

<template>
  <div v-if="stale" class="ui-update-notice">
    <Callout tone="info" role="status">
      <span class="ui-update-notice-body">{{ copy.uiUpdated }}<Button variant="secondary" size="sm" @click="reload">{{ copy.uiReload }}</Button></span>
    </Callout>
  </div>
</template>

<style scoped>
.ui-update-notice-body { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: var(--ui-space-2, 8px); }
</style>
