<script setup lang="ts">
/** The column widths of every table of the daily flash, so the figures of the cards stand on one vertical (docs/design/63): the name takes the rest, the figures have a fixed share. */
defineProps<{ hasBudget: boolean }>()
</script>

<template>
  <colgroup>
    <col :style="{ width: hasBudget ? '26%' : '34%' }">
    <col v-for="i in hasBudget ? 7 : 5" :key="i" :style="{ width: hasBudget ? '10.5%' : '13.2%' }">
  </colgroup>
</template>
