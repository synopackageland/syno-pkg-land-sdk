<script setup lang="ts">
// The one-time approval of the executor, said before the password is asked
// for: at a glance, that the App is not Synology's, that DSM runs no
// third-party App with system privileges, and what the App needs them for (its
// own purpose, or the SDK's). What approving does waits behind a disclosure.
// The approval form goes in the default slot; anyone else is told whom to ask.
// layout page: a card centred in place of the App's pages; inline: within a
// settings section or dialog.
import {computed} from 'vue'
import {ChevronRight,ShieldCheck} from '@lucide/vue'
import {delegateCopy} from './copy'
import {uiLocale} from '../i18n'
import type {ExecutorSetup} from './executor-setup'
const props=defineProps<{admin:boolean;locale?:string;files?:boolean;purpose?:string;layout?:'page'|'inline';webapiProfiles?:ExecutorSetup['webapiProfiles']}>()
const copy=computed(()=>delegateCopy(props.locale))
// Chinese and Japanese run sentences together; the others put a space between.
const gap=computed(()=>['zh-TW','zh-CN','ja'].includes(uiLocale(props.locale??'en'))?'':' ')
const lead=computed(()=>[copy.value.consentWhy,props.purpose||(props.files?copy.value.consentPurposeFiles:copy.value.consentPurpose)].join(gap.value))
const points=computed(()=>[copy.value.pointPassword,copy.value.pointLimited,copy.value.pointUpdate,copy.value.pointRevoke])
</script>
<template>
 <div :class="['executor-consent',`executor-consent-${layout??'inline'}`]" data-executor-consent :data-audience="admin?'admin':'user'">
  <span class="executor-consent-icon" aria-hidden="true"><ShieldCheck :size="layout==='page'?24:18" /></span>
  <div class="executor-consent-body">
   <div class="executor-consent-head">
    <p class="executor-consent-title" data-consent-title>{{ admin?copy.consentTitle:copy.consentTitleUser }}</p>
    <p class="executor-consent-lead" data-consent-lead>{{ lead }}</p>
    <p v-if="!admin" class="executor-consent-lead executor-consent-ask" data-consent-ask>{{ copy.consentAsk }}</p>
   </div>
   <ul v-if="webapiProfiles&&Object.keys(webapiProfiles).length" data-webapi-profiles>
    <li v-for="(profile,name) in webapiProfiles" :key="name"><strong>{{ name }}</strong>: {{ profile.purpose }}
     <details><summary>{{ copy.details }}</summary><div v-for="(methods,api) in profile.apis" :key="api"><code>{{ api }}</code><div v-for="(versions,method) in methods" :key="method">{{ method }} ({{ versions.join(', ') }})</div></div><code v-if="profile.raw_max_bytes" data-webapi-raw>{{ copy.rawLimit }}: {{ profile.raw_max_bytes }}</code></details>
    </li>
   </ul>
   <slot />
   <details v-if="admin" class="executor-consent-more" data-consent-points>
    <summary><ChevronRight class="executor-consent-chevron" :size="14" aria-hidden="true" />{{ copy.consentDetails }}</summary>
    <ul><li v-for="point in points" :key="point">{{ point }}</li></ul>
   </details>
  </div>
 </div>
</template>
<style scoped>
.executor-consent {display:grid;grid-template-columns:32px minmax(0,1fr);column-gap:var(--ui-space-3,12px);max-width:640px;overflow-wrap:anywhere;}
.executor-consent-icon {display:flex;align-items:center;justify-content:center;width:32px;height:32px;border-radius:50%;background:var(--ui-primary-soft);color:var(--ui-primary);}
.executor-consent-body {display:flex;flex-direction:column;gap:var(--ui-space-4,16px);min-width:0;}
.executor-consent-title {margin:6px 0 var(--ui-space-1,4px);font-size:var(--ui-font-size-lg,14px);font-weight:600;line-height:var(--ui-line-height,20px);color:var(--ui-text);}
/* Essential text keeps the full foreground; DSM's muted token is below 4.5:1. */
.executor-consent-lead {margin:0;font-size:var(--ui-font-size,13px);line-height:var(--ui-line-height,20px);color:var(--ui-text);text-wrap:pretty;}
.executor-consent-ask {margin-top:var(--ui-space-2,8px);}
/* The page card: icon over a centred heading, the form below it at reading width. */
.executor-consent-page {grid-template-columns:minmax(0,1fr);justify-items:center;row-gap:var(--ui-space-3,12px);box-sizing:border-box;width:100%;max-width:480px;margin:var(--ui-space-6,24px) auto;padding:var(--ui-space-6,24px);border:1px solid var(--ui-border-subtle);border-radius:var(--ui-radius-lg);background:var(--ui-surface);box-shadow:var(--ui-shadow-card);}
.executor-consent-page .executor-consent-icon {width:48px;height:48px;}
.executor-consent-page .executor-consent-body {width:100%;gap:var(--ui-space-5,20px);}
.executor-consent-page .executor-consent-head {text-align:center;}
.executor-consent-page .executor-consent-title {margin-top:0;font-size:var(--ui-font-size-title,16px);line-height:24px;}
.executor-consent-more summary {display:inline-flex;align-items:center;gap:var(--ui-space-1,4px);cursor:pointer;list-style:none;font-size:var(--ui-font-size,13px);line-height:var(--ui-line-height,20px);color:var(--ui-primary);border-radius:var(--ui-radius-sm);}
.executor-consent-more summary::-webkit-details-marker {display:none;}
.executor-consent-more summary:focus-visible {outline:none;box-shadow:var(--ui-shadow-focus);}
.executor-consent-chevron {transition:transform .15s;}
.executor-consent-more[open] .executor-consent-chevron {transform:rotate(90deg);}
.executor-consent-more ul {margin:var(--ui-space-2,8px) 0 0;padding:var(--ui-space-3,12px) var(--ui-space-4,16px) var(--ui-space-3,12px) 32px;border-radius:var(--ui-radius-md);background:var(--ui-surface-subtle);list-style:disc;font-size:var(--ui-font-size,13px);line-height:var(--ui-line-height,20px);color:var(--ui-text);}
.executor-consent-more li + li {margin-top:var(--ui-space-1,4px);}
@media (prefers-reduced-motion:reduce) {.executor-consent-chevron {transition:none;}}
</style>
