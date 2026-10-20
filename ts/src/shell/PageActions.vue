<script setup lang="ts">
// A page's own header buttons, such as a page's refresh. A page registered with
// AppShell as a component cannot fill the shell's #actions slot, so it renders
// this anywhere in its template: the buttons go to the page header, before the
// App's actions, and leave with the page. Outside an AppShell it renders nothing.
import {computed, inject} from 'vue'
import {PageActionsTarget} from './page-actions'

const target = inject(PageActionsTarget, null)
const to = computed(() => target?.value ?? null)
</script>

<template>
  <Teleport v-if="to" :to="to"><slot /></Teleport>
</template>
