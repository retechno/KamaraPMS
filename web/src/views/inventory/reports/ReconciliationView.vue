<script setup lang="ts">
import { computed, watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryReconciliation, InventoryReconColumn } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import GlCheckPanel from '@/components/inventory/GlCheckPanel.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import { Badge } from '@/components/ui/badge'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { formatMoney, formatPercent } from '@/utils/format'

/**
 * The reconciliation of the food and beverage cost (docs/architecture/35-inventory.md 12.1, 4.9, mockup 30): the opening stock, the purchases, the cost that moved between food and beverage (measured, never
 * posted), the closing stock, the cost of goods used, the staff meals and the entertainment out of it, the net cost of sales against the revenue and against the theory, and what explains the difference.
 */
const r = useInventoryReport<InventoryReconciliation>('/api/v1/properties/{propertyId}/inventory/reports/fb-reconciliation', 'fb-reconciliation.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/fb-reconciliation', { params: { path: { propertyId }, query: { from: p.from, to: p.to } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })

const money = (v: string | undefined): string => (v === undefined || Number(v) === 0 ? '–' : formatMoney(v))
const pct = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : formatPercent(v))

interface Line {
  id: string
  label: string
  cell: (c: InventoryReconColumn) => string
  strong?: boolean
  sign?: '+' | '−' | '='
  indent?: boolean
}
const lines = computed<Line[]>(() => {
  const basis = r.data.value?.closing_basis ?? 'BOOK'
  return [
    { id: 'opening', label: t('inventory.reports.recon.opening'), cell: (c) => money(c.opening) },
    { id: 'openingPosted', label: t('inventory.reports.recon.openingPosted'), cell: (c) => money(c.opening_posted) },
    { id: 'purchases', label: t('inventory.reports.recon.purchases'), sign: '+', cell: (c) => money(c.purchases) },
    { id: 'transferIn', label: t('inventory.reports.recon.transferIn'), sign: '+', cell: (c) => money(c.transfer_in) },
    { id: 'transferOut', label: t('inventory.reports.recon.transferOut'), sign: '−', cell: (c) => money(c.transfer_out) },
    { id: 'closing', label: t(basis === 'COUNT' ? 'inventory.reports.recon.closingCount' : 'inventory.reports.recon.closingBook'), sign: '−', cell: (c) => money(c.closing) },
    { id: 'goodsUsed', label: t('inventory.reports.recon.goodsUsed'), sign: '=', strong: true, cell: (c) => money(c.goods_used) },
    { id: 'staffMeal', label: t('inventory.reports.recon.staffMeal'), sign: '−', cell: (c) => money(c.staff_meal) },
    { id: 'entertainment', label: t('inventory.reports.recon.entertainment'), sign: '−', cell: (c) => money(c.entertainment) },
    { id: 'netCost', label: t('inventory.reports.recon.netCost'), sign: '=', strong: true, cell: (c) => money(c.net_cost) },
    { id: 'revenue', label: t('inventory.reports.recon.revenue'), cell: (c) => money(c.revenue) },
    { id: 'banquetRevenue', label: t('inventory.reports.recon.banquetRevenue'), indent: true, cell: (c) => money(c.banquet_revenue) },
    { id: 'actualPct', label: t('inventory.reports.recon.actualPct'), strong: true, cell: (c) => pct(c.actual_pct) },
    { id: 'theory', label: t('inventory.reports.recon.theory'), cell: (c) => money(c.theory) },
    { id: 'banquetTheory', label: t('inventory.reports.recon.banquetTheory'), indent: true, cell: (c) => money(c.banquet_theory) },
    { id: 'theoryPct', label: t('inventory.reports.recon.theoryPct'), cell: (c) => pct(c.theory_pct) },
    { id: 'target', label: t('inventory.reports.target'), cell: (c) => pct(c.target_pct) },
    { id: 'difference', label: t('inventory.reports.recon.difference'), strong: true, cell: (c) => (Number(c.difference) === 0 ? '–' : `${money(c.difference)}${c.difference_points ? ` (${Number(c.difference_points) > 0 ? '+' : ''}${formatPercent(c.difference_points)})` : ''}`) },
    { id: 'waste', label: t('inventory.reports.recon.waste'), indent: true, cell: (c) => money(c.waste) },
    { id: 'countVariance', label: t('inventory.reports.recon.countVariance'), indent: true, cell: (c) => money(c.count_variance) },
    { id: 'noRecipe', label: t('inventory.reports.recon.noRecipe'), indent: true, cell: (c) => money(c.no_recipe_estimate) },
    { id: 'other', label: t('inventory.reports.recon.other'), indent: true, cell: (c) => money(c.other_outflows) },
  ]
})
const cols = computed(() => (r.data.value ? [{ id: 'food', c: r.data.value.food }, { id: 'beverage', c: r.data.value.beverage }, { id: 'total', c: r.data.value.total }] : []))
</script>

<template>
  <PageHeader :title="t('inventory.reports.recon.title')" :description="t('inventory.reports.recon.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv()" />
    <div v-if="r.data.value" class="flex flex-col gap-4">
      <div class="flex flex-wrap items-center gap-2 text-sm">
        <Badge :variant="r.data.value.closing_basis === 'COUNT' ? 'success' : 'outline'" data-testid="closing-basis">{{ t(`inventory.reports.basis.${r.data.value.closing_basis}`) }}</Badge>
        <span class="text-xs text-muted-foreground">{{ t(`inventory.reports.recon.basisNote.${r.data.value.closing_basis}`) }}</span>
      </div>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[34rem] text-sm" data-testid="recon-table">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.recon.line') }}</th>
              <th v-for="c in cols" :key="c.id" class="py-1 pr-2 text-right font-medium">{{ t(`inventory.reports.recon.col.${c.id}`) }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="l in lines" :key="l.id" class="border-t border-border" :class="l.strong ? 'bg-muted/40 font-semibold' : ''" :data-testid="`recon-${l.id}`">
              <td class="py-1.5 pr-2" :class="l.indent ? 'pl-5 text-muted-foreground' : ''"><span v-if="l.sign" class="mr-1 inline-block w-3 text-muted-foreground" aria-hidden="true">{{ l.sign }}</span>{{ l.label }}</td>
              <td v-for="c in cols" :key="c.id" class="py-1.5 pr-2 text-right" :data-testid="`recon-${l.id}-${c.id}`">{{ l.cell(c.c) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="m-0 text-xs text-muted-foreground">{{ t('inventory.reports.recon.note') }}</p>
      <p class="m-0 text-xs text-muted-foreground" data-testid="recon-recipe-basis">
        {{ t('inventory.reports.reconNote') }}
        <template v-if="r.data.value.total.no_recipe_days > 0"> · <strong data-testid="recon-no-recipe-days">{{ t('inventory.reports.reconNoRecipeDays', { n: r.data.value.total.no_recipe_days }) }}</strong></template>
      </p>
      <GlCheckPanel :check="r.data.value.gl" />
    </div>
  </template>
</template>
