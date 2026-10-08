<script setup lang="ts">
import type { Arrival } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

/**
 * The readiness of an arrival for check-in, as the server worked it out with the rules the check-in uses: READY, or the blockers one by one. It is not a status of the reservation, and the
 * check-in still validates. A room that is not waiting for check-in has none.
 */
defineProps<{ readiness: Arrival['readiness'] }>()
</script>

<template>
  <span v-if="readiness.status !== 'NONE'" class="inline-flex flex-wrap gap-1" data-testid="readiness" :data-status="readiness.status">
    <Badge v-if="readiness.status === 'READY'" variant="success">{{ t('frontDesk.arrivals.readiness.READY') }}</Badge>
    <template v-else>
      <Badge v-for="b in readiness.blockers" :key="b" variant="warning" :data-blocker="b">{{ t(`frontDesk.arrivals.readiness.${b}`) }}</Badge>
    </template>
  </span>
</template>
