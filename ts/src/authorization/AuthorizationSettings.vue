<script setup lang="ts">
import {ref} from 'vue'
import {Button,Dialog,SettingsRow,SettingsSection} from '@synopackageland/ui'
import {ShieldCheck} from '@lucide/vue'
import {useAuthorization,type AuthorizationGateState} from './use-authorization'
// A section for the App's settings page; it reads and revokes through the Gate around it.
const {state,requireAuthorization,text,busy,canManage,revoke}=useAuthorization()
const confirmRevoke=ref(false)
const labels:Record<AuthorizationGateState,string>={checking:'checking',ready:'ready',authorization_required:'state.required',not_admin:'not_admin.title',session_expired:'session_expired.title',unavailable:'state.unavailable'}
</script>
<template>
 <SettingsSection :title="text('settings.title')" :collapsible="false">
  <SettingsRow :label="text('settings.status')" :description="text('purpose')" align="center">
   <div class="authorization-setting">
    <span class="authorization-state" role="status"><ShieldCheck v-if="state==='ready'" :size="16" aria-hidden="true"/>{{ text(labels[state]) }}</span>
    <Button v-if="state==='ready'&&canManage()" variant="secondary" :disabled="busy" @click="confirmRevoke=true">{{ text('revoke') }}</Button>
    <Button v-else-if="state!=='ready'&&state!=='checking'" variant="secondary" @click="requireAuthorization()">{{ text('settings.details') }}</Button>
   </div>
  </SettingsRow>
 </SettingsSection>
 <Dialog v-model:open="confirmRevoke" :title="text('revoke.title')">
  <p class="authorization-revoke-body">{{ text('revoke.body') }}</p>
  <template #footer><Button variant="secondary" @click="confirmRevoke=false">{{ text('cancel') }}</Button><Button variant="danger" :disabled="busy" @click="confirmRevoke=false;revoke()">{{ text('revoke') }}</Button></template>
 </Dialog>
</template>
<style scoped>
.authorization-setting{display:flex;align-items:center;flex-wrap:wrap;gap:var(--ui-space-3,12px)}
.authorization-revoke-body{margin:0;font-size:var(--ui-font-size,13px);line-height:var(--ui-line-height,20px);color:var(--ui-text)}
.authorization-state{display:inline-flex;align-items:center;gap:6px;color:var(--ui-text)}
</style>
