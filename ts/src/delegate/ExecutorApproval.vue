<script setup lang="ts">
// The executor's approval in the window's content, in place of the App's
// pages: until the provided executor (ExecutorSetupKey) reads ready, an
// administrator approves it here with the user password (ExecutorSettings),
// and anyone else is told to ask one. A later status that is not ready, or
// one read on reopen, brings it back. Without an executor there is nothing
// to approve. On a full page (Portal or standalone) it names the App with
// ctx.displayName and ctx.appIcon, as a DSM window's title bar does. A
// stopped or starting package waits for no approval: it says so and checks
// again on its own until the executor answers. With preload the
// App's pages mount while the first status is read, hidden and inert, so
// their own reads run alongside it; a ready status shows that same instance,
// any other unmounts it and discards what it loaded. purpose is the App's own
// sentence for what it needs system privileges for.
import {computed,inject,onBeforeUnmount,onMounted,provide,ref} from 'vue'
import {Callout,LoadingArea} from '@synopackageland/ui'
import {isFullPage,type Context} from '../context'
import ExecutorSettings from './ExecutorSettings.vue'
import ExecutorConsent from './ExecutorConsent.vue'
import type {CapabilityState,CapabilityStatus} from './executor-setup'
import {ExecutorPreloadKey,ExecutorSetupKey,confirmFailure} from './use-executor-setup'
import {delegateCopy} from './copy'
import {appLifecycle,lifecycleCopy} from './lifecycle'
import {useLifecycleRetry} from './use-lifecycle-retry'
const props=defineProps<{admin:boolean;locale?:string;purpose?:string;ctx?:Pick<Context,'reopen'>&Partial<Pick<Context,'displayName'|'appIcon'|'fullPage'|'host'>>;preload?:boolean}>()
const setup=inject(ExecutorSetupKey,undefined)
const copy=computed(()=>delegateCopy(props.locale))
const state=ref<CapabilityState|'checking'>('checking'),current=ref<CapabilityStatus>()
// A context that tells neither its launch nor its host is taken for a window.
const brand=computed(()=>{const ctx=props.ctx,page=ctx?.fullPage??(ctx?.host?isFullPage({host:ctx.host}):false);return page&&ctx?.displayName?ctx:undefined})
function show(next:CapabilityStatus){current.value=next;state.value=next.state}
let disposed=false,stopReopen:(()=>void)|undefined
// A later check keeps what is shown until it answers, so the content stays mounted.
async function check(){
 try{const next=await setup!.status();if(!disposed)show(next)}
 catch{if(!disposed){current.value=undefined;state.value='blocked'}}
}
const lifecycle=computed(()=>appLifecycle(current.value))
const preloads=computed(()=>!!props.preload&&!!setup&&(state.value==='ready'||state.value==='checking'))
provide(ExecutorPreloadKey,computed(()=>preloads.value&&state.value==='checking'))
useLifecycleRetry(()=>lifecycle.value,check)
onMounted(()=>{
 if(!setup)return
 setup.subscribe?.(next=>{if(!disposed&&!confirmFailure(next))show(next)})
 stopReopen=props.ctx?.reopen(()=>{void check()})
 void check()
})
onBeforeUnmount(()=>{disposed=true;stopReopen?.()})
</script>

<template>
  <div v-if="preloads" :class="['executor-pages',{'executor-preloading':state==='checking'}]" :inert="state==='checking'||undefined" :aria-hidden="state==='checking'?'true':undefined"><slot /></div>
  <slot v-else-if="!setup||state==='ready'" />
  <LoadingArea v-if="setup&&state==='checking'" :text="copy.loading" />
  <div v-else-if="setup&&state!=='ready'" class="executor-approval">
    <header v-if="brand" class="executor-brand"><img v-if="brand.appIcon" :src="brand.appIcon" alt="" /><span>{{ brand.displayName }}</span></header>
    <Callout v-if="lifecycle" tone="info" role="status">{{ lifecycleCopy(lifecycle,locale) }}</Callout>
    <ExecutorSettings v-else-if="admin" :admin="true" :locale="locale" :initial="current" :purpose="purpose" title="" layout="page" @confirmed="check" />
    <ExecutorConsent v-else-if="state==='needs-admin-approval'" :admin="false" :locale="locale" :purpose="purpose" layout="page" />
    <Callout v-else tone="info">{{ copy.executorBlocked }}</Callout>
  </div>
</template>

<style scoped>
/* Ready pages lay out as if the wrapper were not there; preloading ones have
   the window's size, for components that measure, but are never seen. */
.executor-pages { display: contents; }
.executor-pages.executor-preloading { display: block; position: fixed; inset: 0; visibility: hidden; overflow: hidden; pointer-events: none; }
.executor-approval { padding: var(--ui-space-6, 24px); }
.executor-brand { display: flex; align-items: center; gap: var(--ui-space-2, 8px); margin-bottom: var(--ui-space-4, 16px); font-size: var(--ui-font-size-title, 16px); font-weight: 600; color: var(--ui-text); }
.executor-brand img { width: 24px; height: 24px; }
</style>
