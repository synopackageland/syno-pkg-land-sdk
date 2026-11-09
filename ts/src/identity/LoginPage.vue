<script setup lang="ts">
import {computed, nextTick, onUnmounted, ref, watchEffect} from 'vue'
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
const httpsURL = ref<string>()
watchEffect(onCleanup => {
 httpsURL.value = undefined
 if (secure.value) return
 const controller = new AbortController()
 onCleanup(() => controller.abort())
 void props.session.readHttpsURL(window.location.href, controller.signal).then(url => {
  if (!controller.signal.aborted) httpsURL.value = url
 })
})
function openHTTPS() {if (httpsURL.value) window.location.assign(httpsURL.value)}
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
 if (busy.value || !secure.value) return
 busy.value = true; error.value = ''
 try {await props.session.bootstrap()}
 catch (e) {if (!disposed) error.value = message(e)}
 finally {if (!disposed) busy.value = false}
}
// Only a successful {enabled:true} offers the App's own sign-in; otherwise this page
// never shows an account/password form (DSM-only App, or identity.status unreadable).
const offersSignIn = computed(() => props.session.enabled.value && !props.session.denied.value)
const retrying = ref(false)
async function retry() {
 if (retrying.value) return
 retrying.value = true
 try {await props.session.retry()} finally {if (!disposed) retrying.value = false}
}
function restart() {password.value = ''; otp.value = ''; needsOTP.value = false; error.value = ''}
onUnmounted(() => {disposed = true; password.value = ''; otp.value = ''})
</script>

<template>
 <main class="identity-login" :aria-busy="busy">
  <SettingsSection :title="copy.title" :description="copy.intro" :collapsible="false">
   <Callout v-if="!secure" tone="warning" :title="copy.httpsTitle" dismissible dismiss-scope="occurrence">
    {{ copy.httpsReason }}
    <p>{{ copy.httpsInstructions }}</p>
    <template v-if="httpsURL" #actions><Button variant="primary" @click="openHTTPS">{{ copy.openHTTPS }}</Button></template>
   </Callout>
   <Callout v-if="session.denied.value" tone="danger"><span role="alert">{{ session.denied.value.message }}</span></Callout>
   <p v-else-if="session.notice.value" role="status">{{ copy.notices[session.notice.value] }}</p>
   <FormActions v-if="!session.enabled.value && session.unavailable.value && !session.denied.value">
    <Button variant="primary" :disabled="retrying" data-identity-retry @click="retry">{{ copy.retry }}</Button>
   </FormActions>
   <Callout v-if="error&&!session.denied.value" tone="danger" :title="copy.failedTitle"><span role="alert">{{ error }}</span></Callout>
   <Form v-if="offersSignIn" @submit.prevent="submit">
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
.identity-login {max-width: 36rem; margin-inline: auto; padding: var(--ui-space-4, 16px); overflow: auto; max-height: 100%; background: var(--ui-bg); color: var(--ui-text); font-family: var(--ui-font-sans); font-size: var(--ui-font-size, 13px); line-height: var(--ui-line-height, 20px);}
.identity-login p {margin-block: var(--ui-space-4, 16px); overflow-wrap: anywhere;}
</style>
