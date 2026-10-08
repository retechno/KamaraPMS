<script setup lang="ts">
import type { InHouseRow } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

/** What is known before a check-out (from the folios), as a badge. It is not a promise: the check-out applies every rule. */
defineProps<{ checkout: InHouseRow['checkout'] }>()

const variants = { READY: 'success', BALANCE_DUE: 'warning', COMPANY_BILL: 'secondary', CHARGES_PENDING: 'secondary', FOLIO_ISSUE: 'destructive' } as const
</script>

<template>
  <Badge :variant="variants[checkout.status]" :data-status="checkout.status" :title="checkout.uncharged_nights ? t('frontDesk.departures.unchargedNights', { n: checkout.uncharged_nights }) : undefined">{{ t(`frontDesk.departures.checkout.${checkout.status}`) }}</Badge>
</template>
