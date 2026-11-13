<script setup lang="ts">
// A page's own controls at the start of the page bar, such as a volume or
// device picker. A page AppShell lays out renders this anywhere in its
// template: the controls go before the page's PageInfo (on the title line with
// header: true) and leave with the page. In the mobile layout a PageMeta with
// a `menu` puts those entries in the phone bar's "…" instead of its controls;
// without one the controls stay in the page bar. Outside an AppShell it renders
// nothing.
import {computed, inject, onBeforeUnmount, watch} from 'vue'
import type {DropdownMenuEntry} from '@synopackageland/ui'
import {PageMetaMenu, PageMetaTarget} from './page-meta'

const props = defineProps<{
  /** The controls as menu entries, for the phone bar's "…" (a submenu with `checked` entries for a picker). */
  menu?: readonly DropdownMenuEntry[]
}>()
const emit = defineEmits<{select: [key: string]}>()
const target = inject(PageMetaTarget, null)
const host = inject(PageMetaMenu, null)
const to = computed(() => target?.value ?? null)
const collapsed = computed(() => !!host?.collapsed.value && !!props.menu?.length)
let release: (() => void) | undefined
watch(() => !!props.menu?.length, wanted => {
  if(wanted && host) release ??= host.register(() => props.menu ?? [], key => emit('select', key))
  else {release?.(); release = undefined}
}, {immediate: true})
onBeforeUnmount(() => release?.())
</script>

<template>
  <Teleport v-if="to && !collapsed" :to="to"><slot /></Teleport>
</template>
