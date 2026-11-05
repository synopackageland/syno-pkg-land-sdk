<script setup lang="ts">
// Read inside the shell's AppRoot, so a caller outside that root can use the
// same narrow-container + coarse-pointer signal as the UI's phone controls.
import {usePhone} from '@synopackageland/ui'
const phone = usePhone()
defineSlots<{default: (scope: {phone: boolean}) => unknown}>()
</script>

<template><slot :phone="phone" /></template>
