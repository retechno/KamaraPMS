<script setup lang="ts">
import { watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryOutletCost } from '@/api/types'
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
 * Food and beverage cost per outlet (docs/architecture/35-inventory.md 12.4, mockup 29): the theory (the recipes times what was sold, at the cost of the day) beside the actual cost of the outlet, which is the theory
 * plus what the counts of its warehouse found. Without an approved count the actual is estimated from the book stock and says so; the difference then is nil.
 */
const r = useInventoryReport<InventoryOutletCost>('/api/v1/properties/{propertyId}/inventory/reports/outlet-cost', 'fb-cost-per-outlet.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/outlet-cost', { params: { path: { propertyId }, query: { from: p.from, to: p.to } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })

const money = (v: string | undefined): string => (v === undefined || Number(v) === 0 ? '–' : formatMoney(v))
const pct = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : formatPercent(v))
const over = (actual: string | null, target: string | null): boolean => actual !== null && target !== null && Number(actual) > Number(target)
</script>

<template>
  <PageHeader :title="t('inventory.reports.outlet.title')" :description="t('inventory.reports.outlet.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv()" />
    <p v-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="outlet-empty">{{ t('inventory.reports.empty') }}</p>
    <div v-else-if="r.data.value" class="overflow-x-auto">
      <table class="w-full min-w-[60rem] text-sm" data-testid="outlet-table">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.outlet.outlet') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.outlet.class') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.outlet.revenue') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.outlet.theory') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.outlet.theoryPct') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.outlet.actual') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.outlet.actualPct') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.outlet.difference') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.target') }}</th>
            <th class="py-1 font-medium">{{ t('inventory.reports.outlet.basis') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(x, n) in r.data.value.rows" :key="n" class="border-t border-border align-top" :data-testid="`outlet-row-${x.outlet_code || 'none'}-${x.class}`">
            <td class="py-2 pr-2">{{ x.outlet_name || t('inventory.reports.outlet.noOutlet') }}<span v-if="x.warehouse_name" class="block text-xs text-muted-foreground">{{ x.warehouse_name }}</span></td>
            <td class="py-2 pr-2">{{ t(`inventory.reports.class.${x.class}`) }}</td>
            <td class="py-2 pr-2 text-right">{{ money(x.revenue) }}<span v-if="Number(x.no_recipe_revenue)" class="block text-xs text-muted-foreground">{{ t('inventory.reports.outlet.noRecipe', { amount: formatMoney(x.no_recipe_revenue) }) }}</span></td>
            <td class="py-2 pr-2 text-right">{{ money(x.theory) }}</td>
            <td class="py-2 pr-2 text-right">{{ pct(x.theory_pct) }}<span v-if="x.no_recipe_days > 0" class="block text-xs font-normal text-muted-foreground" :data-testid="`norecipe-${x.outlet_code || 'none'}-${x.class}`">{{ x.no_recipe_data ? t('inventory.reports.noRecipe') : t('inventory.reports.noRecipeDays', { n: x.no_recipe_days }) }}</span></td>
            <td class="py-2 pr-2 text-right">{{ money(x.actual) }}<span v-if="x.basis === 'BOOK'" class="block text-xs text-muted-foreground" :data-testid="`book-${x.outlet_code}-${x.class}`">{{ t('inventory.reports.estimatedFromBook') }}</span></td>
            <td class="py-2 pr-2 text-right" :class="over(x.actual_pct, x.target_pct) ? 'font-semibold text-destructive' : ''"><span v-if="over(x.actual_pct, x.target_pct)" :title="t('inventory.reports.aboveTarget')">▲ </span>{{ pct(x.actual_pct) }}</td>
            <td class="py-2 pr-2 text-right">{{ money(x.difference) }}</td>
            <td class="py-2 pr-2 text-right text-muted-foreground">{{ pct(x.target_pct) }}</td>
            <td class="py-2"><Badge :variant="x.basis === 'COUNT' ? 'success' : 'outline'">{{ t(`inventory.reports.basis.${x.basis}`) }}</Badge></td>
          </tr>
        </tbody>
      </table>
      <p class="mt-3 text-xs text-muted-foreground">{{ t('inventory.reports.outlet.note') }}</p>
      <GlCheckPanel class="mt-4" :check="r.data.value.gl" />
    </div>
  </template>
</template>
