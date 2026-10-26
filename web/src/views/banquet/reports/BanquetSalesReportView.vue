<script setup lang="ts">
import { watch } from 'vue'
import { api } from '@/api/client'
import type { BanquetSalesReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { formatMoney, formatPercent } from '@/utils/format'

/**
 * Production by sales person (docs/architecture/36-banquet.md 4.10, mockup 50): the events each person opened, made definite and lost in the period, the contract value of the definite and of the lost, and the
 * conversion: definite / (definite + lost). An event that is still open is in neither count.
 */
const r = useInventoryReport<BanquetSalesReport>('/api/v1/properties/{propertyId}/banquet/reports/sales', 'banquet-sales.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/reports/sales', { params: { path: { propertyId }, query: { from: p.from, to: p.to } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const money = (v: string): string => (Number(v) === 0 ? '–' : formatMoney(v))
const pct = (v: string | null): string => (v === null ? '–' : formatPercent(v))
</script>

<template>
  <PageHeader :title="t('banquet.reports.sales.title')" :description="t('banquet.reports.sales.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv()" />
    <p v-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="sales-empty">{{ t('banquet.reports.empty') }}</p>
    <div v-else-if="r.data.value" class="overflow-x-auto">
      <table class="w-full min-w-[40rem] text-sm" data-testid="sales-table">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('banquet.reports.sales.person') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.sales.created') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.sales.definite') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.sales.lost') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.sales.definiteValue') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.sales.lostValue') }}</th>
            <th class="py-1 text-right font-medium">{{ t('banquet.reports.sales.conversion') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="x in r.data.value.rows" :key="x.sales_user_id" class="border-t border-border" :data-testid="`sales-row-${x.sales_user_id}`">
            <td class="py-1.5 pr-2 font-medium">{{ x.sales_name }}</td><td class="py-1.5 pr-2 text-right">{{ x.created }}</td><td class="py-1.5 pr-2 text-right">{{ x.definite }}</td><td class="py-1.5 pr-2 text-right">{{ x.lost }}</td>
            <td class="py-1.5 pr-2 text-right">{{ money(x.definite_value) }}</td><td class="py-1.5 pr-2 text-right text-muted-foreground">{{ money(x.lost_value) }}</td>
            <td class="py-1.5 text-right font-medium">{{ pct(x.conversion_pct) }}</td>
          </tr>
        </tbody>
        <tfoot>
          <tr class="border-t border-border font-semibold" data-testid="sales-total">
            <td class="py-1.5 pr-2">{{ t('banquet.reports.total') }}</td><td class="py-1.5 pr-2 text-right">{{ r.data.value.total.created }}</td><td class="py-1.5 pr-2 text-right">{{ r.data.value.total.definite }}</td><td class="py-1.5 pr-2 text-right">{{ r.data.value.total.lost }}</td>
            <td class="py-1.5 pr-2 text-right">{{ money(r.data.value.total.definite_value) }}</td><td class="py-1.5 pr-2 text-right">{{ money(r.data.value.total.lost_value) }}</td>
            <td class="py-1.5 text-right">{{ pct(r.data.value.total.conversion_pct) }}</td>
          </tr>
        </tfoot>
      </table>
      <p class="mt-3 text-xs text-muted-foreground">{{ t('banquet.reports.sales.note') }}</p>
    </div>
  </template>
</template>
