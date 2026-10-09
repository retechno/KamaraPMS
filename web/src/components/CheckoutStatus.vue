<script setup lang="ts">
import { computed } from 'vue'
import type { InHouseRow } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

/**
 * What is known before a check-out (from the folios), as a badge. It is not a promise: the check-out applies every rule. A guest folio that is below zero holds the guest's money (a deposit that
 * is more than the charges): it has to be settled before the check-out like a balance due, but it is a credit to give back, and the badge says so.
 */
const props = defineProps<{ checkout: InHouseRow['checkout']; folios?: InHouseRow['balance']['folios'] }>()

const credit = computed(() => props.checkout.status === 'BALANCE_DUE' && (props.folios ?? []).some((f) => f.folio_type === 'GUEST' && f.status === 'OPEN' && Number(f.balance) < 0))
const key = computed(() => (credit.value ? 'CREDIT_TO_SETTLE' : props.checkout.status))
const variants = { READY: 'success', BALANCE_DUE: 'warning', CREDIT_TO_SETTLE: 'secondary', COMPANY_BILL: 'secondary', CHARGES_PENDING: 'secondary', FOLIO_ISSUE: 'destructive' } as const
</script>

<template>
  <Badge :variant="variants[key]" :data-status="checkout.status" :data-credit="credit || undefined" :title="checkout.uncharged_nights ? t('frontDesk.departures.unchargedNights', { n: checkout.uncharged_nights }) : undefined">{{ t(`frontDesk.departures.checkout.${key}`) }}</Badge>
</template>
