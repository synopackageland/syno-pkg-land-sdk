<script setup lang="ts">
import {computed} from 'vue'
import {delegateCopy} from './copy'
import type {CapabilityStatus} from './executor-setup'
const props=defineProps<{status?:CapabilityStatus;locale?:string}>()
const copy=computed(()=>delegateCopy(props.locale))
const diff=computed(()=>props.status?.approval)
const visible=computed(()=>props.status?.state==='needs-admin-approval'&&(props.status.code==='task_outdated'||!!diff.value))
const permissions=computed(()=>diff.value?.permissions==='unchanged'?copy.value.upgradeUnchanged:diff.value?.permissions==='expanded'?copy.value.upgradeExpanded:copy.value.upgradeReview)
const short=(value:string|null)=>value?.slice(0,12)??'—'
</script>
<template>
 <section v-if="visible" class="executor-upgrade" data-approval-diff :aria-label="copy.upgradeTitle">
  <p class="executor-upgrade-title">{{ copy.upgradeTitle }}</p>
  <template v-if="diff">
   <p v-if="diff.root_programs.length">{{ copy.upgradePrograms }}</p>
   <p v-if="diff.task_template_changed">{{ copy.upgradeTasks }}</p>
   <p v-if="diff.old_policy_digest!==diff.new_policy_digest&&!diff.root_programs.length">{{ copy.upgradePolicy }}</p>
   <p>{{ permissions }}</p>
   <dl>
    <template v-for="program in diff.root_programs" :key="program.name"><dt>{{ program.name }}</dt><dd><code>{{ short(program.old) }} → {{ short(program.new) }}</code></dd></template>
    <dt>{{ copy.upgradePolicyLabel }}</dt><dd><code>{{ short(diff.old_policy_digest) }} → {{ short(diff.new_policy_digest) }}</code></dd>
   </dl>
   <details v-if="diff.policy_changes.length"><summary>{{ copy.upgradeChanges }}</summary><ul><li v-for="change in diff.policy_changes" :key="change.path"><code>{{ change.path }}</code><pre>{{ JSON.stringify(change.old) }} → {{ JSON.stringify(change.new) }}</pre></li></ul></details>
  </template>
  <p v-else>{{ copy.upgradeUnknown }}</p>
 </section>
</template>
<style scoped>
.executor-upgrade {padding:var(--ui-space-3,12px);border-radius:var(--ui-radius-md);background:var(--ui-surface-subtle);font-size:var(--ui-font-size,13px);line-height:var(--ui-line-height,20px);color:var(--ui-text);overflow-wrap:anywhere;}
.executor-upgrade p {margin:0;}
.executor-upgrade p + p {margin-top:var(--ui-space-1,4px);}
.executor-upgrade .executor-upgrade-title {font-weight:600;margin-bottom:var(--ui-space-2,8px);}
.executor-upgrade dl {display:grid;grid-template-columns:minmax(0,1fr);gap:var(--ui-space-1,4px);margin:var(--ui-space-2,8px) 0 0;}
.executor-upgrade dt {font-weight:600;}
.executor-upgrade dd {margin:0 0 var(--ui-space-1,4px);}
.executor-upgrade code {font-size:var(--ui-font-size-sm,12px);}
.executor-upgrade details {margin-top:var(--ui-space-2,8px);}
.executor-upgrade summary {cursor:pointer;color:var(--ui-primary);}
.executor-upgrade ul {padding-left:var(--ui-space-4,16px);margin:var(--ui-space-2,8px) 0 0;}
.executor-upgrade pre {white-space:pre-wrap;overflow-wrap:anywhere;margin:var(--ui-space-1,4px) 0;}
</style>
