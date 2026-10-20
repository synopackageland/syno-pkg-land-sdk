<script setup lang="ts">
import {onBeforeUnmount,onMounted,ref,shallowRef} from 'vue'
import {Button,Dialog} from '@synopackageland/ui'
import type {SharedFolderPermission,SharedFolderPermissionIssue} from './permission'

const props=defineProps<{permission:SharedFolderPermission}>()
const open=ref(false),issues=shallowRef<readonly SharedFolderPermissionIssue[]>([])
let stop:(()=>void)|undefined
onMounted(()=>{
 stop=props.permission.subscribe(next=>{issues.value=next;open.value=next.length>0})
 // Failure to read status is not evidence that a share needs a permission.
 // The App stays usable; real I/O failures can still trigger this dialog.
 void props.permission.check().catch(()=>{})
})
onBeforeUnmount(()=>{stop?.()})
</script>
<template>
 <Dialog v-if="open" v-model:open="open" :title="permission.messages.pending" :description="permission.messages.pendingNote" :close-label="permission.messages.dismiss">
  <ul class="shared-folder-permission-issues">
   <li v-for="issue in issues" :key="JSON.stringify([issue.account,issue.share,issue.required])">{{ permission.message(issue) }}</li>
  </ul>
  <template #footer>
   <Button variant="secondary" @click="permission.openControlPanel()">{{ permission.messages.openControlPanel }}</Button>
   <Button variant="primary" @click="open=false">{{ permission.messages.dismiss }}</Button>
  </template>
 </Dialog>
</template>
<style scoped>
.shared-folder-permission-issues { display:grid; gap:var(--ui-space-3,12px); padding-inline-start:var(--ui-space-5,20px); overflow-wrap:anywhere; }
</style>
