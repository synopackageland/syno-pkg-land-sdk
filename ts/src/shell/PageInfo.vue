<script setup lang="ts">
// A page's notes behind one info button beside its title, so they take no row
// of their own. A page AppShell lays out renders this anywhere in its template:
// the button goes to the title line, after the title, and leaves with the page.
// `inline` keeps it in place, beside the control its notes explain, as does a
// page shown without the shell. The notes open on a click, or on focus from the
// keyboard, below the button from its start and inside the App's window;
// Escape closes them.
import {computed, inject} from 'vue'
import {InfoTip} from '@synopackageland/ui'
import {format} from '../i18n'
import {shellCopy} from './i18n'
import {PageInfoTarget, ShellLocale} from './page-info'

defineOptions({inheritAttrs: false})
const props = withDefaults(defineProps<{
  notes?: readonly string[]
  inline?: boolean
  // The button's name: "Notes (2)" in the shell's language, or in this locale.
  locale?: string
  label?: string
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
}>(), {side: 'bottom', align: 'start'})
const slots = defineSlots<{default?: () => unknown}>()
const shown = computed(() => (props.notes ?? []).filter(Boolean))
const target = props.inline ? null : inject(PageInfoTarget, null)
// The template would unwrap target itself, and !target would then read "no
// shell" until the shell's title line mounts: the button would first render in
// place and move later. In the shell it waits for its place, where the UI takes
// it as the page's info button (UI 0.20.1+ lists dismissed callouts in it).
const inShell = target !== null
const to = computed(() => target?.value ?? null)
const show = computed(() => (shown.value.length > 0 || !!slots.default) && (!inShell || to.value !== null))
const shellLocale = inject(ShellLocale, null)
const label = computed(() => {
  if(props.label) return props.label
  const locale = props.locale ?? shellLocale?.value ?? 'en'
  return slots.default ? shellCopy(locale).notes : format(shellCopy(locale).notesCount, {count: shown.value.length}, locale)
})
</script>

<template>
  <Teleport v-if="show" :to="to ?? 'body'" :disabled="!inShell">
    <InfoTip v-bind="$attrs" class="page-info" icon="info" trigger-action="click" :side="side" :align="align" :aria-label="label">
      <slot><ul class="page-info-notes"><li v-for="note in shown" :key="note">{{ note }}</li></ul></slot>
    </InfoTip>
  </Teleport>
</template>

<style scoped>
.page-info-notes { display: grid; gap: 6px; margin: 0; padding-left: 14px; list-style: disc; white-space: pre-line; }
</style>
