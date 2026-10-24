<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryPriceVarianceReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { formatMoney, formatPercent } from '@/utils/format'
import { formatReportQty } from '@/utils/inventory'

/** The price on the invoice against the price of the goods received, by supplier and by item (docs/architecture/35-inventory.md 12.5, 22): the difference, its share of the value and how many bills went above the tolerance. */
const r = useInventoryReport<InventoryPriceVarianceReport>('/api/v1/properties/{propertyId}/inventory/reports/price-variances', 'price-variances.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/price-variances', { params: { path: { propertyId }, query: { from: p.from, to: p.to } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const open = ref<Record<number, boolean>>({})
const pct = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : formatPercent(v))
const signed = (v: string): string => (Number(v) > 0 ? `+${formatMoney(v)}` : formatMoney(v))
</script>

<template>
  <PageHeader :title="t('inventory.reports.priceVar.title')" :description="t('inventory.reports.priceVar.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv()" />
    <p v-if="r.loaded.value && !r.data.value?.suppliers.length" class="m-0 text-sm text-muted-foreground" data-testid="pricevar-empty">{{ t('inventory.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <div class="mb-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4" data-testid="pricevar-kpi">
        <KpiCard :label="t('inventory.reports.priceVar.variance')" :value="signed(r.data.value.variance)" :hint="t('inventory.reports.priceVar.ofValue', { pct: pct(r.data.value.pct) })" data-testid="kpi-variance" />
        <KpiCard :label="t('inventory.reports.priceVar.bills')" :value="r.data.value.bills" data-testid="kpi-bills" />
        <KpiCard :label="t('inventory.reports.priceVar.above')" :value="r.data.value.bills_above_tolerance" :tone="r.data.value.bills_above_tolerance ? 'warning' : 'default'" :hint="t('inventory.reports.priceVar.tolerance', { pct: formatPercent(r.data.value.tolerance_pct) })" data-testid="kpi-above" />
        <KpiCard :label="t('inventory.reports.priceVar.billed')" :value="formatMoney(r.data.value.bill_cost)" :hint="t('inventory.reports.priceVar.received', { amount: formatMoney(r.data.value.receipt_value) })" />
      </div>
      <div class="flex flex-col gap-3">
        <section v-for="s in r.data.value.suppliers" :key="s.supplier_id" class="rounded-xl border border-border bg-card p-3" :data-testid="`supplier-${s.supplier_id}`">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 class="m-0 text-sm font-semibold">{{ s.supplier_name }}</h2>
            <span class="text-sm"><strong>{{ signed(s.variance) }}</strong> ({{ pct(s.pct) }})</span>
          </div>
          <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('inventory.reports.priceVar.supplierLine', { bills: s.bills, above: s.bills_above_tolerance, billed: formatMoney(s.bill_cost), received: formatMoney(s.receipt_value) }) }}</p>
          <button type="button" class="mt-2 text-xs underline print:hidden" :aria-expanded="!!open[s.supplier_id]" :data-testid="`supplier-toggle-${s.supplier_id}`" @click="open[s.supplier_id] = !open[s.supplier_id]">{{ t(open[s.supplier_id] ? 'inventory.reports.usage.hide' : 'inventory.reports.usage.show', { n: s.items.length }) }}</button>
          <div :class="open[s.supplier_id] ? 'mt-2 overflow-x-auto' : 'mt-2 hidden overflow-x-auto print:block'">
            <table class="w-full min-w-[40rem] text-sm">
              <thead><tr class="text-left text-xs text-muted-foreground">
                <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.priceVar.poPrice') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.priceVar.billPrice') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.priceVar.variance') }}</th><th class="py-1 pr-2 text-right font-medium">%</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.priceVar.billsTitle') }}</th>
              </tr></thead>
              <tbody>
                <tr v-for="x in s.items" :key="x.item_id" class="border-t border-border">
                  <td class="py-1.5 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }} · {{ formatReportQty(x.qty) }} {{ x.unit_code }}</span></td>
                  <td class="py-1.5 pr-2 text-right">{{ x.receipt_unit_price ? formatMoney(x.receipt_unit_price) : '–' }}</td><td class="py-1.5 pr-2 text-right">{{ x.bill_unit_price ? formatMoney(x.bill_unit_price) : '–' }}</td>
                  <td class="py-1.5 pr-2 text-right">{{ signed(x.variance) }}</td><td class="py-1.5 pr-2 text-right">{{ pct(x.pct) }}<span v-if="x.lines_above_tolerance" class="ml-1" :title="t('inventory.reports.priceVar.aboveMark')">▲</span></td><td class="py-1.5 text-right">{{ x.bills }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </template>
  </template>
</template>
