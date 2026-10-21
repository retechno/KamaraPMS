<script setup lang="ts">
import { reactive, watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryMealsReport } from '@/api/types'
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

/** Staff meals and entertainment by department (docs/architecture/35-inventory.md 12.5, 22): the value, the documents and, where the portions are filled in, the cost of a portion. A reversal takes its document off. */
const lookups = useReportLookups({ departments: true })
const filter = reactive({ department: '' })
const extra = (): Record<string, unknown> => ({ department_id: filter.department ? Number(filter.department) : undefined })

const r = useInventoryReport<InventoryMealsReport>('/api/v1/properties/{propertyId}/inventory/reports/meals', 'meals.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/meals', { params: { path: { propertyId }, query: { from: p.from, to: p.to, ...extra() } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const money = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : formatMoney(v))
</script>

<template>
  <PageHeader :title="t('inventory.reports.meals.title')" :description="t('inventory.reports.meals.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv(extra())">
      <ReportFilterSelect v-model="filter.department" name="department_id" :label="t('inventory.reports.filter.department')" :all-label="t('inventory.reports.filter.allDepartments')" :options="lookups.departments.value.map((d) => ({ id: d.id, label: departmentLabel(d) }))" />
    </ReportPeriodBar>
    <p v-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="meals-empty">{{ t('inventory.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <ul class="m-0 mb-4 grid list-none gap-3 p-0 sm:grid-cols-2" data-testid="meals-totals">
        <li v-for="(x, k) in r.data.value.totals" :key="k" class="rounded-xl border border-border bg-card p-3" :data-testid="`total-${k}`">
          <strong class="block text-sm">{{ t(`inventory.reports.meals.purpose.${k}`) }}</strong>
          <span class="text-lg font-semibold">{{ formatMoney(x.value) }}</span>
          <span class="block text-xs text-muted-foreground">{{ t('inventory.reports.meals.line', { docs: x.docs, covers: x.covers }) }} · {{ t('inventory.reports.meals.perCover') }} {{ money(x.cost_per_cover) }}</span>
        </li>
      </ul>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[40rem] text-sm" data-testid="meals-table">
          <thead><tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.filter.department') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.meals.kind') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.meals.docs') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.meals.covers') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.value.value') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.meals.perCover') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="(x, n) in r.data.value.rows" :key="n" class="border-t border-border">
              <td class="py-1.5 pr-2">{{ x.department_name || t('inventory.reports.noDepartment') }}</td><td class="py-1.5 pr-2">{{ t(`inventory.reports.meals.purpose.${x.purpose}`) }}</td>
              <td class="py-1.5 pr-2 text-right">{{ x.docs }}</td><td class="py-1.5 pr-2 text-right">{{ x.covers || '–' }}</td><td class="py-1.5 pr-2 text-right">{{ formatMoney(x.value) }}</td><td class="py-1.5 text-right">{{ money(x.cost_per_cover) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </template>
</template>
