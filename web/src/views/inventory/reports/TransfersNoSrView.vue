<script setup lang="ts">
import { reactive, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import type { InventoryTransferNoSRReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportFilterSelect from '@/components/inventory/ReportFilterSelect.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { Badge } from '@/components/ui/badge'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { useReportLookups } from '@/composables/useReportLookups'
import { t } from '@/i18n'
import { formatDate, formatMoney } from '@/utils/format'
import { formatReportQty } from '@/utils/inventory'

/** Transfers made without a requisition (docs/architecture/35-inventory.md 12.5, 22): free transfers by period, warehouse, item, value and who made them. A reversed transfer is listed and not counted. */
const lookups = useReportLookups({ warehouses: true })
const filter = reactive({ from: '', to: '' })
const num = (s: string): number | undefined => (s ? Number(s) : undefined)
const extra = (): Record<string, unknown> => ({ from_warehouse_id: num(filter.from), to_warehouse_id: num(filter.to) })

const r = useInventoryReport<InventoryTransferNoSRReport>('/api/v1/properties/{propertyId}/inventory/reports/transfers-without-requisition', 'transfers-without-requisition.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/transfers-without-requisition', { params: { path: { propertyId }, query: { from: p.from, to: p.to, ...extra() } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const options = () => lookups.warehouses.value.map((w) => ({ id: w.id, label: `${w.code} · ${w.name}` }))
</script>

<template>
  <PageHeader :title="t('inventory.reports.trfNoSr.title')" :description="t('inventory.reports.trfNoSr.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv(extra())">
      <ReportFilterSelect v-model="filter.from" name="from_warehouse_id" :label="t('inventory.reports.trfNoSr.fromWarehouse')" :all-label="t('inventory.reports.filter.allWarehouses')" :options="options()" />
      <ReportFilterSelect v-model="filter.to" name="to_warehouse_id" :label="t('inventory.reports.trfNoSr.toWarehouse')" :all-label="t('inventory.reports.filter.allWarehouses')" :options="options()" />
    </ReportPeriodBar>
    <p v-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="trfnosr-empty">{{ t('inventory.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[52rem] text-sm" data-testid="trfnosr-table">
          <thead><tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.date') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.trfNoSr.transfer') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.trfNoSr.route') }}</th>
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.usage.qty') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.value.value') }}</th><th class="py-1 font-medium">{{ t('inventory.reports.trfNoSr.by') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="(x, n) in r.data.value.rows" :key="n" class="border-t border-border" :class="x.status === 'REVERSED' ? 'text-muted-foreground' : ''">
              <td class="py-1.5 pr-2">{{ formatDate(x.business_date) }}</td>
              <td class="py-1.5 pr-2"><RouterLink :to="`/inventory/transfers/${x.transfer_id}`" class="underline">{{ x.number }}</RouterLink><Badge v-if="x.status === 'REVERSED'" variant="secondary" class="ml-1">{{ t('inventory.reports.trfNoSr.reversed') }}</Badge></td>
              <td class="py-1.5 pr-2">{{ x.from_warehouse }} → {{ x.to_warehouse }}</td>
              <td class="py-1.5 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }}</span></td>
              <td class="py-1.5 pr-2 text-right">{{ formatReportQty(x.qty) }} {{ x.unit_code }}</td><td class="py-1.5 pr-2 text-right">{{ formatMoney(x.value) }}</td><td class="py-1.5">{{ x.created_by_name }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="m-0 mt-3 text-right text-sm font-semibold" data-testid="trfnosr-total">{{ t('inventory.reports.total') }}: {{ formatMoney(r.data.value.total) }} · {{ t('inventory.reports.trfNoSr.docs', { n: r.data.value.docs }) }}</p>
    </template>
  </template>
</template>
