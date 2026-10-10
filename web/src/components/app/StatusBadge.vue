<script setup lang="ts">
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import { te, t } from '@/i18n'
import { labelOf } from '@/i18n/labels'
import { knownStatus, OCCUPANCY_ICON, statusVariant, type StatusDomain } from './statusMap'

/** A status as a coloured, translated badge: `<StatusBadge domain="housekeeping" status="DIRTY" />`. Occupancy has an icon instead of a colour. */
const props = defineProps<{ status: string; domain: StatusDomain; label?: string }>()

// A word that means something else in a domain (a stay that is OPEN is "in house") has its own label `statusByDomain.<domain>_<STATUS>`.
const text = computed(() => {
  if (props.label) return props.label
  const own = `statusByDomain.${props.domain}_${props.status}`
  return te(own) ? t(own as never) : labelOf('status', props.status)
})
const icon = computed(() => (props.domain === 'occupancy' ? OCCUPANCY_ICON[props.status] : undefined))
const variant = computed(() => (knownStatus(props.domain, props.status) ? statusVariant(props.domain, props.status) : 'outline'))
</script>

<template>
  <Badge :variant="variant" :data-status="status" data-slot="status-badge"><component :is="icon" v-if="icon" aria-hidden="true" />{{ text }}</Badge>
</template>
