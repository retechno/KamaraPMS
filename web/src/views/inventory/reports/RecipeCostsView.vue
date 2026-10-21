<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryRecipeCostReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportFilterSelect from '@/components/inventory/ReportFilterSelect.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { formatMoney, formatPercent } from '@/utils/format'

/** The recipe cost list (docs/architecture/35-inventory.md 12.5, 22): every product an outlet sells, with the cost of a portion, the net price, the margin and the cost percentage against the target of the outlet; the lowest margin first, the products with no recipe flagged. */
const r = useInventoryReport<InventoryRecipeCostReport>('/api/v1/properties/{propertyId}/inventory/reports/recipe-costs', 'recipe-costs.csv', async (propertyId) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/recipe-costs', { params: { path: { propertyId } } })
  return data ?? null
}, { single: true })
const print = (): void => window.print()
watch(r.pid, r.load, { immediate: true })
const outlet = ref('')
const outlets = computed(() => {
  const seen = new Map<number, string>()
  for (const x of r.data.value?.rows ?? []) seen.set(x.outlet_id, `${x.outlet_code} · ${x.outlet_name}`)
  return [...seen].map(([id, label]) => ({ id, label }))
})
const rows = computed(() => (r.data.value?.rows ?? []).filter((x) => !outlet.value || String(x.outlet_id) === outlet.value))
const money = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : formatMoney(v))
const percent = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : formatPercent(v))
</script>

<template>
  <PageHeader :title="t('inventory.reports.recipeCosts.title')" :description="t('inventory.reports.recipeCosts.intro')" />
  <ReportPrintHeader />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <div class="mb-4 flex flex-wrap items-end gap-3 print:hidden" data-testid="report-period">
      <ReportFilterSelect v-model="outlet" name="outlet_id" :label="t('inventory.reports.filter.outlet')" :all-label="t('inventory.reports.filter.allOutlets')" :options="outlets" />
      <Button type="button" variant="outline" :disabled="r.busy.value" data-testid="report-show" @click="r.load">{{ t('inventory.reports.show') }}</Button>
      <div class="ml-auto flex gap-2">
        <Button type="button" variant="outline" data-testid="report-print" @click="print">{{ t('inventory.reports.print') }}</Button>
        <Button type="button" variant="outline" :disabled="r.busy.value" data-testid="report-csv" @click="r.csv(outlet ? { outlet_id: Number(outlet) } : {})">{{ t('inventory.reports.csv') }}</Button>
      </div>
    </div>
    <p v-if="r.loaded.value && !rows.length" class="m-0 text-sm text-muted-foreground" data-testid="recipecost-empty">{{ t('inventory.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <p v-if="r.data.value.without_recipe" class="m-0 mb-3 text-sm text-warning-text" data-testid="recipecost-missing">{{ t('inventory.reports.recipeCosts.missing', { n: r.data.value.without_recipe }) }}</p>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[52rem] text-sm" data-testid="recipecost-table">
          <thead><tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.recipeCosts.product') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.filter.outlet') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.recipeCosts.cost') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.recipeCosts.price') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.recipeCosts.margin') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.recipeCosts.costPct') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.recipeCosts.target') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="x in rows" :key="`${x.outlet_id}-${x.product_id}`" class="border-t border-border" :data-testid="`recipecost-${x.outlet_code}-${x.product_code}`">
              <td class="py-1.5 pr-2">{{ x.product_name }}<span class="block text-xs text-muted-foreground">{{ x.product_code }} · {{ x.category_name }} · {{ t(`inventory.reports.class.${x.class}`) }}</span></td>
              <td class="py-1.5 pr-2">{{ x.outlet_code }}</td>
              <template v-if="x.has_recipe">
                <td class="py-1.5 pr-2 text-right">{{ money(x.cost) }}</td><td class="py-1.5 pr-2 text-right">{{ money(x.net_price) }}</td><td class="py-1.5 pr-2 text-right">{{ money(x.margin) }}</td>
                <td class="py-1.5 pr-2 text-right" :class="x.above_target ? 'font-semibold text-destructive' : ''">{{ percent(x.cost_pct) }}</td><td class="py-1.5 text-right">{{ percent(x.target_pct) }}</td>
              </template>
              <td v-else colspan="5" class="py-1.5 text-right"><Badge variant="secondary">{{ t('inventory.reports.recipeCosts.noRecipe') }}</Badge></td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </template>
</template>
