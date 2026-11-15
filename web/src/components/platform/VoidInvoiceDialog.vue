<script setup lang="ts">
import type { components } from '@/api/schema'

// placeholder with the final contract; the screen is built in this pull request
defineProps<{ open: boolean; invoice: components['schemas']['PlatformInvoiceRow'] }>()
defineEmits<{ 'update:open': [boolean]; voided: [] }>()
</script>

<template>
  <div v-if="open" data-testid="void-invoice-dialog" />
</template>
