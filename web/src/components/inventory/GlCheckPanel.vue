<script setup lang="ts">
import type { InventoryGlCheck } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'
import { formatDate, formatMoney } from '@/utils/format'

/**
 * The check of the cost reports against the general ledger (docs/architecture/35-inventory.md 6.3, 12.1): for the days that the close has journaled, what the stock recorded on each cost account and what the
 * ledger holds. "Match" or the difference, in words and not only in a colour; a period with no journaled day says that the ledger has not been written yet.
 */
defineProps<{ check: InventoryGlCheck }>()
const money = (v: string): string => (Number(v) === 0 ? '0' : formatMoney(v))
</script>

<template>
  <section class="rounded-xl border border-border bg-card p-4" aria-labelledby="gl-h" data-testid="gl-check">
    <div class="mb-2 flex flex-wrap items-center gap-2">
      <h2 id="gl-h" class="m-0 text-sm font-semibold">{{ t('inventory.reports.gl.title') }}</h2>
      <Badge :variant="check.status === 'MATCH' ? 'success' : check.status === 'DIFFERENCE' ? 'destructive' : 'outline'" data-testid="gl-status">
        {{ check.status === 'MATCH' ? '✓ ' : check.status === 'DIFFERENCE' ? '✕ ' : '' }}{{ t(`inventory.reports.gl.${check.status}`) }}
      </Badge>
      <span v-if="check.gl_through" class="text-xs text-muted-foreground">{{ t('inventory.reports.gl.through', { date: formatDate(check.gl_through) }) }}</span>
    </div>
    <p v-if="check.status === 'PENDING'" class="m-0 text-sm text-muted-foreground" data-testid="gl-pending">{{ t('inventory.reports.gl.pending') }}</p>
    <table v-else class="w-full text-sm">
      <thead>
        <tr class="text-left text-xs text-muted-foreground">
          <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.gl.account') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.gl.stock') }}</th>
          <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.gl.ledger') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.gl.difference') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="l in check.lines" :key="l.key" class="border-t border-border" :data-testid="`gl-${l.key}`">
          <td class="py-1.5 pr-2">{{ t(`inventory.reports.gl.keys.${l.key}`) }}</td>
          <td class="py-1.5 pr-2 text-right">{{ money(l.stock) }}</td><td class="py-1.5 pr-2 text-right">{{ money(l.gl) }}</td>
          <td class="py-1.5 text-right" :class="Number(l.difference) !== 0 ? 'font-semibold text-destructive' : ''">{{ money(l.difference) }}</td>
        </tr>
        <tr class="border-t-2 border-border font-semibold">
          <td class="py-1.5 pr-2">{{ t('inventory.reports.total') }}</td>
          <td class="py-1.5 pr-2 text-right">{{ money(check.stock_total) }}</td><td class="py-1.5 pr-2 text-right">{{ money(check.gl_total) }}</td><td class="py-1.5 text-right">{{ money(check.difference) }}</td>
        </tr>
      </tbody>
    </table>
    <p class="m-0 mt-2 text-xs text-muted-foreground">{{ t('inventory.reports.gl.note') }}</p>
  </section>
</template>
