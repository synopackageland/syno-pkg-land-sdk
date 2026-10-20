<script setup lang="ts">
import {computed, nextTick, onUnmounted, ref} from 'vue'
import {Button, Callout, Form, FormActions, FormItem, Input, SettingsSection} from '@synopackageland/ui'
import type {IdentitySession} from './session'
import {identityCopy} from './i18n'
const props = defineProps<{session: IdentitySession; locale: string}>()
const copy = computed(() => identityCopy(props.locale))
const account = ref('')
const password = ref('')
const otp = ref('')
const needsOTP = ref(false)
const busy = ref(false)
const error = ref('')
const otpField = ref<HTMLElement>()
const secure = computed(() => window.location.protocol === 'https:')
let disposed = false
function message(e: unknown) {
 const code = (e as {code?: string})?.code
 return props.session.denied.value?.message || (copy.value.errors as Record<string, string>)[code || ''] || copy.value.errors.failed
}
async function submit() {
 if (busy.value || !secure.value || !account.value || !password.value || (needsOTP.value && !otp.value)) return
 busy.value = true; error.value = ''
 try {
  await props.session.login(account.value, password.value, otp.value)
  password.value = ''; otp.value = ''
 } catch (e) {
  if (disposed) return
  otp.value = ''
  if ((e as {code?: string})?.code === 'otp_required') {
   needsOTP.value = true
  } else {error.value = message(e); password.value = ''; needsOTP.value = false}
 } finally {
  if (!disposed) {
   busy.value = false
   if (needsOTP.value) {await nextTick(); if (!disposed) otpField.value?.querySelector('input')?.focus()}
  }
 }
}
async function useDSM() {
 if (busy.value) return
 busy.value = true; error.value = ''
 try {await props.session.bootstrap()}
 catch (e) {if (!disposed) error.value = message(e)}
 finally {if (!disposed) busy.value = false}
}
function restart() {password.value = ''; otp.value = ''; needsOTP.value = false; error.value = ''}
onUnmounted(() => {disposed = true; password.value = ''; otp.value = ''})
</script>

<template>
 <main class="identity-login" :aria-busy="busy">
  <SettingsSection :title="copy.title" :description="copy.intro" :collapsible="false">
   <Callout v-if="!secure" tone="warning" :title="copy.httpsTitle">{{ copy.errors.https_required }}</Callout>
   <Callout v-if="session.denied.value" tone="danger"><span role="alert">{{ session.denied.value.message }}</span></Callout>
   <p v-else-if="session.notice.value" role="status">{{ copy.notices[session.notice.value] }}</p>
   <Callout v-if="error&&!session.denied.value" tone="danger" :title="copy.failedTitle"><span role="alert">{{ error }}</span></Callout>
   <Form v-if="!session.denied.value" @submit.prevent="submit">
    <FormItem :label="copy.account"><Input v-model="account" autocomplete="username" :disabled="busy || needsOTP || !secure" /></FormItem>
    <FormItem v-if="!needsOTP" :label="copy.password"><Input v-model="password" type="password" autocomplete="current-password" :disabled="busy || !secure" /></FormItem>
    <template v-else>
     <p role="status">{{ copy.otpPrompt }}</p>
     <div ref="otpField"><FormItem :label="copy.otp"><Input v-model="otp" inputmode="numeric" autocomplete="one-time-code" :disabled="busy" /></FormItem></div>
    </template>
    <FormActions>
     <Button type="submit" variant="primary" :disabled="busy || !secure || !account || !password || (needsOTP && !otp)">{{ busy ? copy.signingIn : needsOTP ? copy.verify : copy.signIn }}</Button>
     <Button v-if="needsOTP" variant="secondary" :disabled="busy" @click="restart">{{ copy.otherAccount }}</Button>
     <Button v-else variant="secondary" :disabled="busy || !secure" @click="useDSM">{{ copy.continueDSM }}</Button>
    </FormActions>
   </Form>
  </SettingsSection>
 </main>
</template>

<style scoped>
.identity-login {max-width: 36rem; margin-inline: auto; padding: var(--ui-space-4, 16px); overflow: auto; max-height: 100%; color: var(--ui-text); font-family: var(--ui-font-sans); font-size: var(--ui-font-size, 13px); line-height: var(--ui-line-height, 20px);}
.identity-login p {margin-block: var(--ui-space-4, 16px); overflow-wrap: anywhere;}
</style>
