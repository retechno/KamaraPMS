<script setup lang="ts">
import { reactive, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import type { InventoryPurchaseReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportFilterSelect from '@/components/inventory/ReportFilterSelect.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { useReportLookups } from '@/composables/useReportLookups'
import { t } from '@/i18n'
import { formatMoney, formatPercent } from '@/utils/format'
import { formatQty } from '@/utils/inventory'

/** Purchases by supplier and by item (docs/architecture/35-inventory.md 12.5, 22.5): the receipts that stand, with the bills matched to them; an item links to its price history. */
const lookups = useReportLookups({ warehouses: true, categories: true, suppliers: true })
const filter = reactive({ supplier: '', warehouse: '', category: '' })
const num = (s: string): number | undefined => (s ? Number(s) : undefined)
const extra = (): Record<string, unknown> => ({ supplier_id: num(filter.supplier), warehouse_id: num(filter.warehouse), category_id: num(filter.category) })

const r = useInventoryReport<InventoryPurchaseReport>('/api/v1/properties/{propertyId}/inventory/reports/purchases', 'purchases.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/purchases', { params: { path: { propertyId }, query: { from: p.from, to: p.to, ...extra() } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const pct = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : formatPercent(v))
</script>

<template>
  <PageHeader :title="t('inventory.reports.purchases.title')" :description="t('inventory.reports.purchases.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv(extra())">
      <ReportFilterSelect v-model="filter.supplier" name="supplier_id" :label="t('inventory.reports.filter.supplier')" :all-label="t('inventory.reports.filter.allSuppliers')" :options="lookups.suppliers.value.map((s) => ({ id: s.id, label: s.name }))" />
      <ReportFilterSelect v-model="filter.warehouse" name="warehouse_id" :label="t('inventory.reports.filter.warehouse')" :all-label="t('inventory.reports.filter.allWarehouses')" :options="lookups.warehouses.value.map((w) => ({ id: w.id, label: `${w.code} · ${w.name}` }))" />
      <ReportFilterSelect v-model="filter.category" name="category_id" :label="t('inventory.reports.filter.category')" :all-label="t('inventory.reports.filter.allCategories')" :options="lookups.categories.value.map((c) => ({ id: c.id, label: c.name }))" />
    </ReportPeriodBar>
    <p v-if="r.loaded.value && !r.data.value?.suppliers.length" class="m-0 text-sm text-muted-foreground" data-testid="purchases-empty">{{ t('inventory.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <div class="mb-4 grid gap-3 sm:grid-cols-3" data-testid="purchases-kpi">
        <KpiCard :label="t('inventory.reports.total')" :value="formatMoney(r.data.value.value)" data-testid="kpi-purchases-total" />
        <KpiCard :label="t('inventory.reports.purchases.receipts')" :value="r.data.value.receipts" data-testid="kpi-purchases-receipts" />
        <KpiCard :label="t('inventory.reports.purchases.billed')" :value="formatMoney(r.data.value.billed_value)" :hint="`${t('inventory.reports.purchases.bills')}: ${r.data.value.bills}`" data-testid="kpi-purchases-billed" />
      </div>
      <section v-for="s in r.data.value.suppliers" :key="s.supplier_id" class="mb-5" :data-testid="`purchase-supplier-${s.supplier_id}`">
        <h3 class="m-0 mb-1 flex flex-wrap items-baseline gap-x-3 text-sm font-semibold">
          {{ s.supplier_name }}
          <span class="text-base">{{ formatMoney(s.value) }}</span>
          <span class="text-xs font-normal text-muted-foreground">{{ pct(s.share_pct) }} · {{ t('inventory.reports.purchases.docs', { receipts: s.receipts, bills: s.bills }) }}</span>
        </h3>
        <div class="overflow-x-auto">
          <table class="w-full min-w-[44rem] text-sm">
            <thead>
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.usage.qty') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.value.value') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.purchases.avgPrice') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.purchases.receipts') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.purchases.billed') }}</th>
                <th class="py-1 font-medium"><span class="sr-only">{{ t('inventory.reports.purchases.history') }}</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="x in s.items" :key="x.item_id" class="border-t border-border" :data-testid="`purchase-${s.supplier_id}-${x.item_code}`">
                <td class="py-1.5 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }} · {{ x.category_name }}</span></td>
                <td class="py-1.5 pr-2 text-right">{{ formatQty(x.qty) }} {{ x.unit_code }}</td>
                <td class="py-1.5 pr-2 text-right">{{ formatMoney(x.value) }}</td>
                <td class="py-1.5 pr-2 text-right">{{ x.avg_price === null ? '–' : formatMoney(x.avg_price) }}</td>
                <td class="py-1.5 pr-2 text-right">{{ x.receipts }}</td>
                <td class="py-1.5 pr-2 text-right">{{ Number(x.billed_value) === 0 ? '–' : formatMoney(x.billed_value) }}</td>
                <td class="py-1.5 text-right print:hidden"><RouterLink :to="{ path: '/inventory/reports/price-history', query: { item: String(x.item_id) } }" :data-testid="`purchase-history-${x.item_code}`">{{ t('inventory.reports.purchases.history') }}</RouterLink></td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
  </template>
</template>
