<script setup lang="ts">
import { reactive, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import type { InventoryPriceHistoryReport } from '@/api/types'
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

/** The price paid for an item at each receipt, per unit of purchase and per unit of stock (docs/architecture/35-inventory.md 12.5, 22.5). The item comes from the list or from the link of the purchases report. */
const route = useRoute()
const lookups = useReportLookups({ items: true, suppliers: true })
const filter = reactive({ item: typeof route.query.item === 'string' ? route.query.item : '', supplier: '' })
const num = (s: string): number | undefined => (s ? Number(s) : undefined)
const extra = (): Record<string, unknown> => ({ item_id: num(filter.item), supplier_id: num(filter.supplier) })

const r = useInventoryReport<InventoryPriceHistoryReport>('/api/v1/properties/{propertyId}/inventory/reports/price-history', 'price-history.csv', async (propertyId, p) => {
  if (!filter.item) return null
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/price-history', {
    params: { path: { propertyId }, query: { from: p.from, to: p.to, item_id: Number(filter.item), supplier_id: num(filter.supplier) } },
  })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const pct = (v: string | null | undefined): string => (v === null || v === undefined ? '' : `${Number(v) > 0 ? '+' : ''}${formatPercent(v)}`)
const price = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : formatMoney(v))
</script>

<template>
  <PageHeader :title="t('inventory.reports.priceHistory.title')" :description="t('inventory.reports.priceHistory.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv(extra())">
      <ReportFilterSelect v-model="filter.item" name="item_id" :label="t('inventory.reports.filter.item')" :all-label="t('inventory.reports.priceHistory.pick')" :options="lookups.items.value.map((i) => ({ id: i.id, label: `${i.code} · ${i.name}` }))" />
      <ReportFilterSelect v-model="filter.supplier" name="supplier_id" :label="t('inventory.reports.filter.supplier')" :all-label="t('inventory.reports.filter.allSuppliers')" :options="lookups.suppliers.value.map((s) => ({ id: s.id, label: s.name }))" />
    </ReportPeriodBar>
    <p v-if="!filter.item" class="m-0 text-sm text-muted-foreground" data-testid="pricehistory-pick">{{ t('inventory.reports.priceHistory.chooseItem') }}</p>
    <p v-else-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="pricehistory-empty">{{ t('inventory.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <h3 class="m-0 mb-2 text-sm font-semibold">{{ r.data.value.item_name }} <span class="font-normal text-muted-foreground">{{ r.data.value.item_code }} · {{ r.data.value.unit_code }}</span></h3>
      <div class="mb-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4" data-testid="pricehistory-kpi">
        <KpiCard :label="t('inventory.reports.priceHistory.min')" :value="price(r.data.value.min_price)" data-testid="kpi-price-min" />
        <KpiCard :label="t('inventory.reports.priceHistory.max')" :value="price(r.data.value.max_price)" data-testid="kpi-price-max" />
        <KpiCard :label="t('inventory.reports.priceHistory.last')" :value="price(r.data.value.last_price)" data-testid="kpi-price-last" />
        <KpiCard :label="t('inventory.reports.priceHistory.avg')" :value="price(r.data.value.avg_price)" data-testid="kpi-price-avg" />
      </div>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[44rem] text-sm" data-testid="pricehistory-table">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.date') }}</th>
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.priceHistory.receipt') }}</th>
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.filter.supplier') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.usage.qty') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.priceHistory.unitPrice') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.priceHistory.perStock', { unit: r.data.value.unit_code }) }}</th>
              <th class="py-1 text-right font-medium">{{ t('inventory.reports.priceHistory.change') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="x in r.data.value.rows" :key="x.receipt_id" class="border-t border-border" :data-testid="`price-${x.receipt_number}`">
              <td class="py-1.5 pr-2">{{ x.received_on }}</td>
              <td class="py-1.5 pr-2">{{ x.receipt_number }}</td>
              <td class="py-1.5 pr-2">{{ x.supplier_name }}</td>
              <td class="py-1.5 pr-2 text-right">{{ formatQty(x.qty_purchase) }} {{ x.purchase_unit_code }}</td>
              <td class="py-1.5 pr-2 text-right">{{ formatMoney(x.unit_price) }}</td>
              <td class="py-1.5 pr-2 text-right">{{ formatMoney(x.price_per_stock) }}</td>
              <td class="py-1.5 text-right">{{ pct(x.change_pct) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </template>
</template>
