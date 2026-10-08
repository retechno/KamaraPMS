<script setup lang="ts">
import type { InHouseRow } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

/** The balance of a stay as the server states it (the folios of the ledger): zero is settled, above zero is owed, below zero is a credit. */
defineProps<{ balance: InHouseRow['balance'] }>()

const variants = { SETTLED: 'outline', OUTSTANDING: 'warning', CREDIT: 'secondary' } as const
</script>

<template>
  <Badge :variant="variants[balance.status]" class="tabular-nums" :data-status="balance.status" data-testid="balance" :title="t(`frontDesk.inHouse.status.${balance.status}`)">{{ $money(balance.amount) }}</Badge>
</template>
