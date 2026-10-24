<script setup lang="ts">
import { reactive, watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryTopUsageReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { NativeSelect } from '@/components/ui/native-select'
import ReportFilterSelect from '@/components/inventory/ReportFilterSelect.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { useReportLookups } from '@/composables/useReportLookups'
import { t } from '@/i18n'
import { formatMoney, formatPercent } from '@/utils/format'
import { formatReportQty } from '@/utils/inventory'

/** The items with the largest value that left in the period (docs/architecture/35-inventory.md 12.5, 22.5), with the share of the total of every item and the running share; top 20 or 50, by kind of use and warehouse. */
const kindList = ['POS_SALE', 'ISSUE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT', 'TRANSFER_OUT', 'SUPPLIER_RETURN']
const lookups = useReportLookups({ warehouses: true, categories: true })
const filter = reactive({ limit: '20', warehouse: '', category: '', kinds: ['POS_SALE', 'ISSUE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT'] })
const num = (s: string): number | undefined => (s ? Number(s) : undefined)
const extra = (): Record<string, unknown> => ({ limit: num(filter.limit), warehouse_id: num(filter.warehouse), category_id: num(filter.category), kinds: filter.kinds.length ? filter.kinds.join(',') : undefined })

const r = useInventoryReport<InventoryTopUsageReport>('/api/v1/properties/{propertyId}/inventory/reports/top-usage', 'top-usage.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/top-usage', { params: { path: { propertyId }, query: { from: p.from, to: p.to, ...extra() } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const kindLabel = (k: string): string => t(`inventory.reports.topUsage.kind.${k}`)
</script>

<template>
  <PageHeader :title="t('inventory.reports.topUsage.title')" :description="t('inventory.reports.topUsage.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv(extra())">
      <FormField :label="t('inventory.reports.topUsage.limit')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="filter.limit" name="limit" class="w-32" @change="r.load">
            <option v-for="n in [20, 50]" :key="n" :value="String(n)">{{ t('inventory.reports.topUsage.top', { n }) }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <ReportFilterSelect v-model="filter.warehouse" name="warehouse_id" :label="t('inventory.reports.filter.warehouse')" :all-label="t('inventory.reports.filter.allWarehouses')" :options="lookups.warehouses.value.map((w) => ({ id: w.id, label: `${w.code} · ${w.name}` }))" />
      <ReportFilterSelect v-model="filter.category" name="category_id" :label="t('inventory.reports.filter.category')" :all-label="t('inventory.reports.filter.allCategories')" :options="lookups.categories.value.map((c) => ({ id: c.id, label: c.name }))" />
      <fieldset class="m-0 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 border-0 p-0" data-testid="top-kinds">
        <legend class="mb-1 p-0 text-sm font-medium">{{ t('inventory.reports.topUsage.kinds') }}</legend>
        <label v-for="k in kindList" :key="k" class="flex items-center gap-1 text-sm"><input v-model="filter.kinds" type="checkbox" :value="k" :name="`kind_${k}`" :data-testid="`top-kind-${k}`" />{{ kindLabel(k) }}</label>
      </fieldset>
    </ReportPeriodBar>
    <p v-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="top-empty">{{ t('inventory.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[44rem] text-sm" data-testid="top-table">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.topUsage.rank') }}</th>
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.usage.qty') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.value.value') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.topUsage.share') }}</th>
              <th class="py-1 text-right font-medium">{{ t('inventory.reports.topUsage.cumulative') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="x in r.data.value.rows" :key="x.item_id" class="border-t border-border" :data-testid="`top-${x.item_code}`">
              <td class="py-1.5 pr-2">{{ x.rank }}</td>
              <td class="py-1.5 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }} · {{ x.category_name }}</span></td>
              <td class="py-1.5 pr-2 text-right">{{ formatReportQty(x.qty) }} {{ x.unit_code }}</td>
              <td class="py-1.5 pr-2 text-right">{{ formatMoney(x.value) }}</td>
              <td class="py-1.5 pr-2 text-right">{{ formatPercent(x.share_pct) }}</td>
              <td class="py-1.5 text-right">{{ formatPercent(x.cumulative_pct) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="m-0 mt-3 text-right text-sm text-muted-foreground" data-testid="top-total">{{ t('inventory.reports.topUsage.shown', { shown: formatMoney(r.data.value.shown), total: formatMoney(r.data.value.total) }) }}</p>
    </template>
  </template>
</template>
