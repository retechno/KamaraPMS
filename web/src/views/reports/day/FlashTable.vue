<script setup lang="ts">
import type { FlashLine } from '@/api/types'
import { t } from '@/i18n'
import { flashValue, flashVariance, rowLabel } from './flashLabels'

/** A block of rows of the daily flash with the seven columns of the mockup (docs/design/63). A row without a budget has no budget and no variance. */
defineProps<{ lines: FlashLine[]; testid: string; heading?: string; total?: FlashLine; totalLabel?: string; hasBudget: boolean }>()
const th = 'whitespace-nowrap px-2 py-1.5 text-right text-[11px] font-medium uppercase text-muted-foreground'
</script>

<template>
  <table class="w-full border-collapse text-sm tabular-nums" :data-testid="testid">
    <thead>
      <tr class="border-b border-border">
        <th class="px-2 py-1.5 text-left text-[11px] font-medium uppercase text-muted-foreground">{{ heading }}</th>
        <th :class="th">{{ t('dayReports.flash.today') }}</th>
        <th :class="th">{{ t('dayReports.flash.mtd') }}</th>
        <th :class="th">{{ t('dayReports.flash.lastMonth') }}</th>
        <th :class="th">{{ t('dayReports.flash.lastYear') }}</th>
        <th v-if="hasBudget" :class="th">{{ t('dayReports.flash.budget') }}</th>
        <th v-if="hasBudget" :class="th">{{ t('dayReports.flash.variance') }}</th>
        <th :class="th">{{ t('dayReports.flash.ytd') }}</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="l in lines" :key="l.key" class="border-b border-border" :data-testid="`flash-row-${l.key}`">
        <td class="px-2 py-1.5">{{ rowLabel(l) }}</td>
        <td class="px-2 py-1.5 text-right">{{ flashValue(l.kind, l.today) }}</td>
        <td class="px-2 py-1.5 text-right">{{ flashValue(l.kind, l.mtd) }}</td>
        <td class="px-2 py-1.5 text-right text-muted-foreground">{{ flashValue(l.kind, l.last_month) }}</td>
        <td class="px-2 py-1.5 text-right text-muted-foreground">{{ flashValue(l.kind, l.last_year) }}</td>
        <td v-if="hasBudget" class="px-2 py-1.5 text-right">{{ flashValue(l.kind, l.budget) }}</td>
        <td v-if="hasBudget" :class="['px-2 py-1.5 text-right', l.favourable === true && 'text-success', l.favourable === false && 'text-destructive']">{{ flashVariance(l) }}</td>
        <td class="px-2 py-1.5 text-right">{{ flashValue(l.kind, l.ytd) }}</td>
      </tr>
    </tbody>
    <tfoot v-if="total">
      <tr class="font-semibold" :data-testid="`flash-total-${total.key}`">
        <td class="px-2 py-1.5">{{ totalLabel ?? rowLabel(total) }}</td>
        <td class="px-2 py-1.5 text-right">{{ flashValue(total.kind, total.today) }}</td>
        <td class="px-2 py-1.5 text-right">{{ flashValue(total.kind, total.mtd) }}</td>
        <td class="px-2 py-1.5 text-right">{{ flashValue(total.kind, total.last_month) }}</td>
        <td class="px-2 py-1.5 text-right">{{ flashValue(total.kind, total.last_year) }}</td>
        <td v-if="hasBudget" class="px-2 py-1.5 text-right">{{ flashValue(total.kind, total.budget) }}</td>
        <td v-if="hasBudget" :class="['px-2 py-1.5 text-right', total.favourable === true && 'text-success', total.favourable === false && 'text-destructive']">{{ flashVariance(total) }}</td>
        <td class="px-2 py-1.5 text-right">{{ flashValue(total.kind, total.ytd) }}</td>
      </tr>
    </tfoot>
  </table>
</template>
