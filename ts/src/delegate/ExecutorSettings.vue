<script setup lang="ts">
import {computed} from 'vue'
import {Badge,Button,Callout,Form,FormItem,Input,PathDisplay,Select,SettingsRow,SettingsSection,Skeleton} from '@synopackageland/ui'
import type {Context} from '../context'
import type {CapabilityStatus} from './executor-setup'
import {useScopeApproval} from './use-scope-approval'
import {delegateCopy} from './copy'
import {appLifecycle,lifecycleCopy} from './lifecycle'
import {useLifecycleRetry} from './use-lifecycle-retry'
import ExecutorConsent from './ExecutorConsent.vue'
// initial: the status ExecutorApproval has just read; the panel shows it without reading it again.
// purpose: the App's own sentence for what it needs system privileges for.
// title replaces the section title; an empty string drops it where a window or dialog already names it.
// layout page centres the approval as a card in place of the App's pages.
const props=defineProps<{admin:boolean;locale?:string;ctx?:Pick<Context,'call'>;initial?:CapabilityStatus;purpose?:string;title?:string;layout?:'page'|'inline'}>()
const emit=defineEmits<{confirmed:[]}>()
const copy=computed(()=>delegateCopy(props.locale))
const {status,password,busy,confirming,error,failure,passwordError,scope,chosen,options,dynamic,files,webapiProfiles,canConfirm,needsScope,refresh,submit,explain}=useScopeApproval(props,()=>emit('confirmed'))
const state=computed(()=>confirming.value?'confirming':!status.value?undefined:status.value.state==='ready'?'ready':'blocked')
const badge=computed(()=>({ready:{tone:'success',text:copy.value.statusReady},blocked:{tone:'danger',text:copy.value.statusBlocked},confirming:{tone:'info',text:copy.value.statusConfirming}} as const)[state.value??'confirming'])
// A stopped or starting package waits for no approval: the panel says so and
// checks again on its own.
const lifecycle=computed(()=>confirming.value?undefined:appLifecycle(status.value))
useLifecycleRetry(()=>lifecycle.value,refresh)
// Something to approve: the panel explains it and asks for the password in
// place of a status that would only repeat it.
const consent=computed(()=>!lifecycle.value&&(status.value?.state==='needs-admin-approval'||needsScope.value))
const form=computed(()=>consent.value||!!status.value?.retry)
// The page header's refresh reloads every panel on the page.
defineExpose({refresh})
</script>
<template>
 <SettingsSection v-if="admin" :title="title??copy.setupTitle" :description="consent?undefined:files?copy.setupHelpFiles:copy.setupHelp" :collapsible="false" :aria-busy="busy||!status&&!confirming&&!error">
  <Callout v-if="error" tone="danger" role="alert"><span class="executor-alert"><span>{{ copy.setupError }}</span><Button variant="secondary" size="sm" :disabled="busy" @click="refresh">{{ copy.retry }}</Button></span></Callout>
  <Callout v-else-if="failure&&!('confirm' in failure)" tone="danger" role="alert">{{ failure.code==='session_ended'?copy.session:explain(failure)??copy.setupError }}<span v-if="failure.reason" class="executor-reason">{{ failure.reason }}</span></Callout>
  <Callout v-if="lifecycle" tone="info" role="status">{{ lifecycleCopy(lifecycle,locale) }}</Callout>
  <template v-else>
   <div v-if="!status&&!confirming&&!error" class="executor-summary"><SettingsRow :label="copy.status" data-placeholder-row><Skeleton width="5rem" height="20px" /></SettingsRow></div>
   <div v-if="(status||confirming)&&!consent" class="executor-summary">
    <SettingsRow :label="copy.status"><span role="status" :data-executor-status="state"><Badge :tone="badge.tone">{{ badge.text }}</Badge></span></SettingsRow>
    <SettingsRow v-if="status?.reason&&(status.state==='blocked'||status.state==='unsupported')" :label="copy.details"><template v-if="explain(status)">{{ explain(status) }}<span class="executor-reason">{{ status.reason }}</span></template><template v-else>{{ status.reason }}</template></SettingsRow>
    <SettingsRow v-if="scope?.selection" :label="copy.protectedScope"><PathDisplay :path="scope.selection.root" /></SettingsRow>
   </div>
   <Callout v-if="state==='blocked'&&!consent" tone="warning">{{ files?copy.blocked:copy.executorBlocked }}</Callout>
   <Callout v-if="scope?.mismatch" tone="warning">{{ copy.scopeMismatch }}</Callout>
   <component :is="consent?ExecutorConsent:'div'" v-if="form" v-bind="consent?{admin:true,locale,files,purpose,layout,webapiProfiles}:{}">
    <Form direction="vertical" class="executor-form" @submit.prevent="submit">
     <FormItem v-if="dynamic" :label="copy.scopeLabel" :description="copy.preselectionHelp"><Select id="delegate-scope" v-model="chosen" :options="options" :disabled="busy||error" /></FormItem>
     <FormItem :label="copy.password" :description="confirming?copy.confirmingHelp:copy.passwordNote" :help="passwordError" :validate-status="passwordError?'error':undefined" validate-message-display-mode="text">
      <Input id="delegate-password" v-model="password" type="password" autocomplete="current-password" :invalid="!!passwordError" :disabled="busy||error" />
      <template #after="{classes}"><Button :class="classes.afterItem" type="submit" variant="primary" :loading="confirming" :disabled="busy||error||!canConfirm">{{ dynamic?copy.confirmShare:copy.authorize }}</Button></template>
     </FormItem>
    </Form>
   </component>
  </template>
 </SettingsSection>
</template>
<style scoped>
.executor-summary:not(:last-child) {margin-bottom:var(--ui-space-3,12px);}
.executor-form {max-width:28rem;}
.executor-summary + .executor-form, .ui-callout + div > .executor-form {margin-top:var(--ui-space-4,16px);}
.executor-reason {display:block;margin-top:var(--ui-space-1,4px);font-size:var(--ui-font-size-sm,12px);}
.executor-alert {display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:var(--ui-space-2,8px);}
</style>
