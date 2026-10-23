<script setup lang="ts">
import { reactive, watch } from 'vue'
import { api } from '@/api/client'
import type { InventorySlowStockReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { NativeSelect } from '@/components/ui/native-select'
import ReportFilterSelect from '@/components/inventory/ReportFilterSelect.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { useReportLookups } from '@/composables/useReportLookups'
import { t } from '@/i18n'
import { formatMoney } from '@/utils/format'
import { formatReportQty } from '@/utils/inventory'

/** Slow and dead stock (docs/architecture/35-inventory.md 12.5, 22.5): the items with stock that nothing consumed for 30, 60, 90 or 180 days, as of the open day, with the age of the stock. */
const lookups = useReportLookups({ warehouses: true, categories: true })
const filter = reactive({ days: '90', warehouse: '', category: '' })
const num = (s: string): number | undefined => (s ? Number(s) : undefined)
const extra = (): Record<string, unknown> => ({ days: num(filter.days), warehouse_id: num(filter.warehouse), category_id: num(filter.category) })

const r = useInventoryReport<InventorySlowStockReport>(
  '/api/v1/properties/{propertyId}/inventory/reports/slow-stock',
  'slow-stock.csv',
  async (propertyId) => {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/slow-stock', { params: { path: { propertyId }, query: extra() } })
    return data ?? null
  },
  { single: true },
)
watch(r.pid, r.load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('inventory.reports.slowStock.title')" :description="t('inventory.reports.slowStock.intro')" />
  <ReportPrintHeader :to="r.data.value?.as_of ?? r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:to="r.period.to" no-date single :busy="r.busy.value" @show="r.load" @csv="r.csv(extra())">
      <FormField :label="t('inventory.reports.slowStock.days')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="filter.days" name="days" class="w-40" @change="r.load">
            <option v-for="d in [30, 60, 90, 180]" :key="d" :value="String(d)">{{ t('inventory.reports.slowStock.daysOption', { n: d }) }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <ReportFilterSelect v-model="filter.warehouse" name="warehouse_id" :label="t('inventory.reports.filter.warehouse')" :all-label="t('inventory.reports.filter.allWarehouses')" :options="lookups.warehouses.value.map((w) => ({ id: w.id, label: `${w.code} · ${w.name}` }))" />
      <ReportFilterSelect v-model="filter.category" name="category_id" :label="t('inventory.reports.filter.category')" :all-label="t('inventory.reports.filter.allCategories')" :options="lookups.categories.value.map((c) => ({ id: c.id, label: c.name }))" />
    </ReportPeriodBar>
    <p v-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="slow-empty">{{ t('inventory.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <div class="mb-4 grid gap-3 sm:grid-cols-3" data-testid="slow-kpi">
        <KpiCard :label="t('inventory.reports.total')" :value="formatMoney(r.data.value.total)" data-testid="kpi-slow-total" />
        <KpiCard :label="t('inventory.reports.item')" :value="r.data.value.rows.length" :hint="t('inventory.reports.slowStock.neverCount', { n: r.data.value.never_out })" data-testid="kpi-slow-count" />
      </div>
      <p class="m-0 mb-2 text-xs text-muted-foreground" data-testid="slow-basis">{{ t(`inventory.reports.slowStock.basis${r.data.value.age_basis}`) }}</p>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[48rem] text-sm" data-testid="slow-table">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th>
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.filter.warehouse') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.usage.qty') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.value.value') }}</th>
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.slowStock.lastOut') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.slowStock.daysSince') }}</th>
              <th class="py-1 text-right font-medium">{{ t('inventory.reports.slowStock.age') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="x in r.data.value.rows" :key="`${x.warehouse_id}-${x.item_id}`" class="border-t border-border" :data-testid="`slow-${x.warehouse_code}-${x.item_code}`">
              <td class="py-1.5 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }} · {{ x.category_name }}</span></td>
              <td class="py-1.5 pr-2">{{ x.warehouse_code }}</td>
              <td class="py-1.5 pr-2 text-right">{{ formatReportQty(x.qty) }} {{ x.unit_code }}</td>
              <td class="py-1.5 pr-2 text-right">{{ formatMoney(x.value) }}</td>
              <td class="py-1.5 pr-2">{{ x.last_out_on ?? '–' }}</td>
              <td class="py-1.5 pr-2 text-right"><span v-if="x.never_out" class="rounded-full bg-muted px-2 py-0.5 text-xs" data-testid="slow-never">{{ t('inventory.reports.slowStock.never') }}</span><template v-else>{{ x.days_since_out }}</template></td>
              <td class="py-1.5 text-right">{{ x.age_days === null ? '–' : t('inventory.reports.slowStock.ageDays', { n: x.age_days }) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </template>
</template>
