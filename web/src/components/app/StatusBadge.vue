<script setup lang="ts">
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import { te, t } from '@/i18n'
import { knownStatus, statusVariant, type StatusDomain } from './statusMap'

/** A status as a coloured, translated badge: `<StatusBadge domain="housekeeping" status="DIRTY" />`. */
const props = defineProps<{ status: string; domain: StatusDomain; label?: string }>()

const text = computed(() => {
  if (props.label) return props.label
  const key = `status.${props.status}`
  return te(key) ? t(key as never) : props.status
})
const variant = computed(() => (knownStatus(props.domain, props.status) ? statusVariant(props.domain, props.status) : 'outline'))
</script>

<template>
  <Badge :variant="variant" :data-status="status" data-slot="status-badge">{{ text }}</Badge>
</template>
