<script setup lang="ts">
import type { LedgerCheck } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { t } from '@/i18n'
import { formatMoney } from '@/utils/format'

/** What the ledger report is checked against: another report of the same day and the account of the general ledger. A difference is shown, it blocks nothing. */
defineProps<{ checks: LedgerCheck[]; reconciled: boolean; asOf: string }>()
const KNOWN = new Set(['FOLIO_BALANCE', 'DEPOSITS_HELD', 'ACCOUNT_GUEST_LEDGER', 'MOVEMENT', 'ACCOUNT_ADVANCE_DEPOSITS'])
const title = (c: LedgerCheck): string => (KNOWN.has(c.key) ? t(`dayReports.ledger.check.${c.key}` as 'dayReports.ledger.check.MOVEMENT') : c.title)
</script>

<template>
  <Card class="p-4" data-testid="ledger-checks">
    <h2 class="mb-2 mt-0 text-sm font-semibold">
      {{ t('dayReports.ledger.checked', { date: asOf }) }}
      <Badge :variant="reconciled ? 'secondary' : 'destructive'" data-testid="reconciled">{{ reconciled ? t('dayReports.matches') : t('dayReports.differs') }}</Badge>
    </h2>
    <table class="w-full border-collapse text-sm tabular-nums">
      <thead>
        <tr class="border-b border-border">
          <th class="px-2 py-1.5 text-left text-[11px] font-medium uppercase text-muted-foreground" />
          <th class="px-2 py-1.5 text-right text-[11px] font-medium uppercase text-muted-foreground">{{ t('dayReports.ledger.thisReport') }}</th>
          <th class="px-2 py-1.5 text-right text-[11px] font-medium uppercase text-muted-foreground">{{ t('dayReports.ledger.other') }}</th>
          <th class="px-2 py-1.5 text-right text-[11px] font-medium uppercase text-muted-foreground">{{ t('dayReports.difference') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="c in checks" :key="c.key" class="border-b border-border" :data-testid="`check-${c.key}`">
          <td class="px-2 py-1.5">{{ title(c) }}<span v-if="c.account" class="text-muted-foreground"> · {{ c.account }}</span></td>
          <td class="px-2 py-1.5 text-right">{{ formatMoney(c.report) }}</td>
          <td class="px-2 py-1.5 text-right">{{ formatMoney(c.other) }}</td>
          <td :class="['px-2 py-1.5 text-right', Number(c.difference) !== 0 && 'text-destructive']" :data-testid="`check-diff-${c.key}`">{{ formatMoney(c.difference) }}</td>
        </tr>
      </tbody>
    </table>
  </Card>
</template>
