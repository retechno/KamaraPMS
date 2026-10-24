<script setup lang="ts">
import { reactive, watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryWasteReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportFilterSelect from '@/components/inventory/ReportFilterSelect.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { departmentLabel } from '@/composables/useDepartments'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { useReportLookups } from '@/composables/useReportLookups'
import { t } from '@/i18n'
import { formatMoney } from '@/utils/format'
import { formatQty } from '@/utils/inventory'

/** Waste by reason (docs/architecture/35-inventory.md 12.5, 22): what was thrown away, by reason, warehouse, department and item, with the dishes thrown away after a POS void (POS_VOID). */
const lookups = useReportLookups({ warehouses: true, categories: true, departments: true })
const filter = reactive({ warehouse: '', department: '', category: '' })
const num = (s: string): number | undefined => (s ? Number(s) : undefined)
const extra = (): Record<string, unknown> => ({ warehouse_id: num(filter.warehouse), department_id: num(filter.department), category_id: num(filter.category) })

const r = useInventoryReport<InventoryWasteReport>('/api/v1/properties/{propertyId}/inventory/reports/waste', 'waste.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/waste', { params: { path: { propertyId }, query: { from: p.from, to: p.to, ...extra() } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const reasonLabel = (reason: string): string => t(`inventory.reports.waste.reason.${reason}`)
</script>

<template>
  <PageHeader :title="t('inventory.reports.waste.title')" :description="t('inventory.reports.waste.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv(extra())">
      <ReportFilterSelect v-model="filter.warehouse" name="warehouse_id" :label="t('inventory.reports.filter.warehouse')" :all-label="t('inventory.reports.filter.allWarehouses')" :options="lookups.warehouses.value.map((w) => ({ id: w.id, label: `${w.code} · ${w.name}` }))" />
      <ReportFilterSelect v-model="filter.department" name="department_id" :label="t('inventory.reports.filter.department')" :all-label="t('inventory.reports.filter.allDepartments')" :options="lookups.departments.value.map((d) => ({ id: d.id, label: departmentLabel(d) }))" />
      <ReportFilterSelect v-model="filter.category" name="category_id" :label="t('inventory.reports.filter.category')" :all-label="t('inventory.reports.filter.allCategories')" :options="lookups.categories.value.map((c) => ({ id: c.id, label: c.name }))" />
    </ReportPeriodBar>
    <p v-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="waste-empty">{{ t('inventory.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <ul class="m-0 mb-4 grid list-none gap-3 p-0 sm:grid-cols-2 xl:grid-cols-3" data-testid="waste-reasons">
        <li v-for="x in r.data.value.reasons" :key="x.reason" class="rounded-xl border border-border bg-card p-3" :data-testid="`reason-${x.reason}`">
          <strong class="block text-sm">{{ reasonLabel(x.reason) }}</strong>
          <span class="text-lg font-semibold">{{ formatMoney(x.value) }}</span>
          <span class="block text-xs text-muted-foreground">{{ t('inventory.reports.waste.docs', { n: x.docs }) }}</span>
        </li>
      </ul>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[52rem] text-sm" data-testid="waste-table">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.waste.reasonTitle') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.filter.warehouse') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.filter.department') }}</th>
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.usage.qty') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.value.value') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.waste.docsTitle') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(x, n) in r.data.value.rows" :key="n" class="border-t border-border">
              <td class="py-1.5 pr-2">{{ reasonLabel(x.reason) }}</td><td class="py-1.5 pr-2">{{ x.warehouse_code }}</td><td class="py-1.5 pr-2">{{ x.department_name || t('inventory.reports.noDepartment') }}</td>
              <td class="py-1.5 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }} · {{ x.category_name }}</span></td>
              <td class="py-1.5 pr-2 text-right">{{ x.reason === 'POS_VOID' ? '–' : `${formatQty(x.qty)} ${x.unit_code}` }}</td><td class="py-1.5 pr-2 text-right">{{ formatMoney(x.value) }}</td><td class="py-1.5 text-right">{{ x.docs }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="m-0 mt-3 text-right text-sm font-semibold" data-testid="waste-total">{{ t('inventory.reports.total') }}: {{ formatMoney(r.data.value.total) }} · {{ t('inventory.reports.waste.docs', { n: r.data.value.docs }) }}</p>
    </template>
  </template>
</template>
