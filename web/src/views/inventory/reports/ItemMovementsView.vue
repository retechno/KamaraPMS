<script setup lang="ts">
import { reactive, watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryItemMovementReport, InventoryQtyValue } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportFilterSelect from '@/components/inventory/ReportFilterSelect.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { useReportLookups } from '@/composables/useReportLookups'
import { t } from '@/i18n'
import { formatMoney } from '@/utils/format'
import { formatReportQty } from '@/utils/inventory'

/**
 * The stock movement per item (docs/architecture/35-inventory.md 12.5, 22.5): the opening, what came in and went out by kind, the count variance and the price adjustments, and the closing. In every row
 * opening + in - out + count variance + adjustments = closing, because the figures come from one ledger.
 */
const lookups = useReportLookups({ warehouses: true, categories: true })
const filter = reactive({ warehouse: '', category: '' })
const num = (s: string): number | undefined => (s ? Number(s) : undefined)
const extra = (): Record<string, unknown> => ({ warehouse_id: num(filter.warehouse), category_id: num(filter.category) })

const r = useInventoryReport<InventoryItemMovementReport>('/api/v1/properties/{propertyId}/inventory/reports/item-movements', 'item-movements.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/item-movements', { params: { path: { propertyId }, query: { from: p.from, to: p.to, ...extra() } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })

const groups: { id: 'in' | 'out' | 'adjust'; buckets: string[] }[] = [
  { id: 'in', buckets: ['OPENING_STOCK', 'RECEIPTS', 'TRANSFERS_IN', 'SALES_RETURNS'] },
  { id: 'out', buckets: ['POS_SALES', 'USAGE', 'WASTE', 'STAFF_MEALS', 'ENTERTAINMENT', 'TRANSFERS_OUT', 'SUPPLIER_RETURNS'] },
  { id: 'adjust', buckets: ['COUNT_VARIANCE', 'COST_ADJUST'] },
]
const buckets = groups.flatMap((g) => g.buckets)
const empty = (v: InventoryQtyValue | undefined): boolean => !v || (Number(v.qty) === 0 && Number(v.value) === 0)
</script>

<template>
  <PageHeader :title="t('inventory.reports.movements.title')" :description="t('inventory.reports.movements.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv(extra())">
      <ReportFilterSelect v-model="filter.warehouse" name="warehouse_id" :label="t('inventory.reports.filter.warehouse')" :all-label="t('inventory.reports.filter.allWarehouses')" :options="lookups.warehouses.value.map((w) => ({ id: w.id, label: `${w.code} · ${w.name}` }))" />
      <ReportFilterSelect v-model="filter.category" name="category_id" :label="t('inventory.reports.filter.category')" :all-label="t('inventory.reports.filter.allCategories')" :options="lookups.categories.value.map((c) => ({ id: c.id, label: c.name }))" />
    </ReportPeriodBar>
    <p v-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="movements-empty">{{ t('inventory.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[96rem] text-sm" data-testid="movements-table">
          <thead>
            <tr class="text-xs text-muted-foreground">
              <th rowspan="2" class="py-1 pr-2 text-left font-medium">{{ t('inventory.reports.item') }}</th>
              <th rowspan="2" class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.movements.opening') }}</th>
              <th v-for="g in groups" :key="g.id" :colspan="g.buckets.length" class="border-l border-border py-1 pr-2 text-center font-medium">{{ t(`inventory.reports.movements.group.${g.id}`) }}</th>
              <th rowspan="2" class="border-l border-border py-1 text-right font-medium">{{ t('inventory.reports.movements.closing') }}</th>
            </tr>
            <tr class="text-xs text-muted-foreground">
              <th v-for="b in buckets" :key="b" class="py-1 pr-2 text-right font-medium">{{ t(`inventory.reports.movements.bucket.${b}`) }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="x in r.data.value.rows" :key="x.item_id" class="border-t border-border align-top" :data-testid="`move-${x.item_code}`">
              <td class="py-1.5 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }} · {{ x.category_name }}</span></td>
              <td class="py-1.5 pr-2 text-right" :data-testid="`move-${x.item_code}-opening`">{{ formatReportQty(x.opening.qty) }} {{ x.unit_code }}<span class="block text-xs text-muted-foreground">{{ formatMoney(x.opening.value) }}</span></td>
              <td v-for="b in buckets" :key="b" class="py-1.5 pr-2 text-right" :data-testid="`move-${x.item_code}-${b}`">
                <template v-if="empty(x.buckets[b])">–</template>
                <template v-else>{{ formatReportQty(x.buckets[b]!.qty) }}<span class="block text-xs text-muted-foreground">{{ formatMoney(x.buckets[b]!.value) }}</span></template>
              </td>
              <td class="py-1.5 text-right font-medium" :data-testid="`move-${x.item_code}-closing`">{{ formatReportQty(x.closing.qty) }} {{ x.unit_code }}<span class="block text-xs font-normal text-muted-foreground">{{ formatMoney(x.closing.value) }}</span></td>
            </tr>
          </tbody>
        </table>
      </div>
      <h3 class="m-0 mb-1.5 mt-6 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('inventory.reports.movements.byCategory') }}</h3>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[96rem] text-sm" data-testid="movements-categories">
          <tbody>
            <tr v-for="c in [...r.data.value.categories, r.data.value.total]" :key="c.category_id ?? 'total'" class="border-t border-border" :class="c.category_id === null ? 'font-semibold' : ''" :data-testid="`movecat-${c.category_id ?? 'total'}`">
              <td class="py-1.5 pr-2">{{ c.category_id === null ? t('inventory.reports.total') : c.category_name }}</td>
              <td class="py-1.5 pr-2 text-right">{{ formatMoney(c.opening) }}</td>
              <td v-for="b in buckets" :key="b" class="py-1.5 pr-2 text-right">{{ Number(c.buckets[b]) === 0 ? '–' : formatMoney(c.buckets[b]!) }}</td>
              <td class="py-1.5 text-right">{{ formatMoney(c.closing) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </template>
</template>
