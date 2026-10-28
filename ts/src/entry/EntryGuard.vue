<script setup lang="ts">
import {computed, onBeforeUnmount, onMounted, ref} from 'vue'
import {Button, Dialog} from '@synopackageland/ui'
import type {Context} from '../context'
import {appRefusal} from '../authorization/refusal'
import {authorizationCopy} from '../authorization/i18n'
import {identityCopy} from '../identity/i18n'
import {createEntryCheck, type EntryFailure, type EntryMode} from './check'

const props = defineProps<{
 ctx: Pick<Context, 'call'> & Partial<Pick<Context, 'reopen' | 'displayName'>>
 mode: EntryMode
 locale?: string
 // Apps may supply their own localized dialog copy.
 messages?: {title: string; session: string; unavailable: string; retry: string; dismiss: string}
}>()
const failure = ref<EntryFailure>()
const open = ref(false)
const busy = ref(false)
const messages = computed(() => {
 if(props.messages) return props.messages
 const identity = identityCopy(props.locale ?? 'en')
 const authorization = authorizationCopy(props.locale ?? 'en', props.ctx.displayName ?? '')
 return {
  title: identity.failedTitle, session: identity.errors.session_required, unavailable: identity.errors.auth_unavailable,
  retry: authorization('retry'), dismiss: authorization('cancel'),
 }
})
const refusal = computed(() => appRefusal(failure.value, props.locale ?? 'en', props.ctx.displayName))
const description = computed(() => refusal.value?.message ?? (failure.value?.code === 'session_required' ? messages.value.session : messages.value.unavailable))
const check = createEntryCheck({call: (...args) => props.ctx.call(...args)}, props.mode, result => {
 failure.value = result
 open.value = result !== undefined
})
let stopReopen: (() => void) | undefined
let disposed = false
async function retry() {
 busy.value = true
 try {await check.check()} finally {if(!disposed) busy.value = false}
}
function shown() {if(document.visibilityState !== 'hidden') void check.check()}
onMounted(() => {
 stopReopen = props.ctx.reopen?.(() => {void check.check()})
 document.addEventListener('visibilitychange', shown)
 void check.check()
})
onBeforeUnmount(() => {
 disposed = true
 check.dispose()
 stopReopen?.()
 document.removeEventListener('visibilitychange', shown)
})
</script>

<template>
 <slot />
 <Dialog v-if="failure && open" v-model:open="open" :title="refusal?.title ?? messages.title" :description="description" :close-label="messages.dismiss">
  <template #footer>
   <Button variant="secondary" @click="open=false">{{ messages.dismiss }}</Button>
   <Button variant="primary" :disabled="busy" @click="retry">{{ messages.retry }}</Button>
  </template>
 </Dialog>
</template>
