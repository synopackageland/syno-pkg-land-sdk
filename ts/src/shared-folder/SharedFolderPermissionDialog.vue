<script setup lang="ts">
import {onBeforeUnmount,onMounted,ref,shallowRef} from 'vue'
import {Button,Dialog} from '@synopackageland/ui'
import type {SharedFolderPermission,SharedFolderPermissionIssue} from './permission'

const props=defineProps<{permission:SharedFolderPermission}>()
const open=ref(false),issues=shallowRef<readonly SharedFolderPermissionIssue[]>([]),busy=ref(false),failed=ref(false)
let disposed=false
let stop:(()=>void)|undefined
onMounted(()=>{
 stop=props.permission.subscribe(next=>{issues.value=next;open.value=next.length>0})
 // Failure to read status is not evidence that a share needs a permission.
 // The App stays usable; real I/O failures can still trigger this dialog.
 void props.permission.check().catch(()=>{})
})
onBeforeUnmount(()=>{disposed=true;stop?.()})
async function retry(){
 if(busy.value)return
 busy.value=true;failed.value=false
 try{await props.permission.retry()}
 catch{if(!disposed)failed.value=true}
 finally{if(!disposed)busy.value=false}
}
</script>
<template>
 <Dialog v-if="open" v-model:open="open" :title="permission.messages.pending" :description="permission.messages.pendingNote" :close-label="permission.messages.dismiss">
  <ul class="shared-folder-permission-issues">
   <li v-for="issue in issues" :key="JSON.stringify([issue.account,issue.share,issue.required])">{{ permission.message(issue) }}</li>
  </ul>
  <p v-if="failed" role="alert">{{ permission.messages.shared_folder_unavailable }}</p>
  <template #footer>
   <Button variant="secondary" @click="permission.openControlPanel()">{{ permission.messages.openControlPanel }}</Button>
   <Button variant="secondary" @click="open=false">{{ permission.messages.dismiss }}</Button>
   <Button variant="primary" :disabled="busy" @click="retry">{{ permission.messages.retry }}</Button>
  </template>
 </Dialog>
</template>
<style scoped>
.shared-folder-permission-issues { display:grid; gap:var(--ui-space-3,12px); padding-inline-start:var(--ui-space-5,20px); overflow-wrap:anywhere; }
</style>
