<script setup lang="ts">
import type { InHouseRow } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

/**
 * The balance of a stay as the server states it (the folios of the ledger): zero is settled, above zero is owed, below zero is a credit. A stay without a folio says "No folio" and a balance
 * that was not given says "Unavailable": neither is ever shown as zero.
 */
defineProps<{ balance?: InHouseRow['balance'] | null }>()

const variants = { SETTLED: 'outline', OUTSTANDING: 'warning', CREDIT: 'secondary', NO_FOLIO: 'destructive' } as const
</script>

<template>
  <Badge v-if="!balance" variant="outline" data-status="UNAVAILABLE" data-testid="balance">{{ t('frontDesk.inHouse.unavailable') }}</Badge>
  <Badge v-else-if="balance.status === 'NO_FOLIO'" :variant="variants.NO_FOLIO" data-status="NO_FOLIO" data-testid="balance">{{ t('frontDesk.inHouse.noFolio') }}</Badge>
  <Badge v-else :variant="variants[balance.status]" class="tabular-nums" :data-status="balance.status" data-testid="balance" :title="t(`frontDesk.inHouse.status.${balance.status}`)">{{ $money(balance.amount) }}</Badge>
</template>
