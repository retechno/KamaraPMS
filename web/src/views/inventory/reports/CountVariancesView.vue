<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import type { InventoryCountVarianceReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportFilterSelect from '@/components/inventory/ReportFilterSelect.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { useReportLookups } from '@/composables/useReportLookups'
import { t } from '@/i18n'
import { formatDate, formatMoney } from '@/utils/format'
import { formatQty } from '@/utils/inventory'

/** The variances of the approved counts (docs/architecture/35-inventory.md 12.5, 22): by count (date, warehouse, who approved, the plus and the minus) and by item (book, found, difference, value). */
type View = 'COUNTS' | 'LINES' | 'ITEMS'
const view = ref<View>('COUNTS')
const lookups = useReportLookups({ warehouses: true })
const filter = reactive({ warehouse: '' })
const extra = (): Record<string, unknown> => ({ warehouse_id: filter.warehouse ? Number(filter.warehouse) : undefined })

const r = useInventoryReport<InventoryCountVarianceReport>('/api/v1/properties/{propertyId}/inventory/reports/count-variances', 'count-variances.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/count-variances', { params: { path: { propertyId }, query: { from: p.from, to: p.to, ...extra() } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const signed = (v: string): string => (Number(v) > 0 ? `+${formatMoney(v)}` : formatMoney(v))
const tabs: { id: View; label: string }[] = [
  { id: 'COUNTS', label: 'inventory.reports.countVar.byCount' },
  { id: 'LINES', label: 'inventory.reports.countVar.byItem' },
  { id: 'ITEMS', label: 'inventory.reports.countVar.itemTotals' },
]
</script>

<template>
  <PageHeader :title="t('inventory.reports.countVar.title')" :description="t('inventory.reports.countVar.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv(extra())">
      <ReportFilterSelect v-model="filter.warehouse" name="warehouse_id" :label="t('inventory.reports.filter.warehouse')" :all-label="t('inventory.reports.filter.allWarehouses')" :options="lookups.warehouses.value.map((w) => ({ id: w.id, label: `${w.code} · ${w.name}` }))" />
    </ReportPeriodBar>
    <p v-if="r.loaded.value && !r.data.value?.counts.length" class="m-0 text-sm text-muted-foreground" data-testid="countvar-empty">{{ t('inventory.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <div class="mb-3 flex flex-wrap items-center gap-4 text-sm" data-testid="countvar-totals">
        <span>{{ t('inventory.reports.countVar.gain') }}: <strong>{{ formatMoney(r.data.value.gain) }}</strong></span>
        <span>{{ t('inventory.reports.countVar.loss') }}: <strong>{{ formatMoney(r.data.value.loss) }}</strong></span>
        <span>{{ t('inventory.reports.countVar.net') }}: <strong>{{ signed(r.data.value.net) }}</strong></span>
      </div>
      <div role="tablist" class="mb-3 flex flex-wrap gap-1.5 print:hidden" :aria-label="t('inventory.reports.countVar.views')">
        <button v-for="tab in tabs" :key="tab.id" type="button" role="tab" :aria-selected="view === tab.id" class="rounded-md border border-border px-3 py-1.5 text-sm" :class="view === tab.id ? 'bg-primary text-primary-foreground' : 'bg-card'" :data-testid="`tab-${tab.id}`" @click="view = tab.id">{{ t(tab.label) }}</button>
      </div>
      <div v-if="view === 'COUNTS'" class="overflow-x-auto" data-testid="countvar-counts">
        <table class="w-full min-w-[44rem] text-sm">
          <thead><tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.date') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.countVar.count') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.filter.warehouse') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.countVar.approver') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.countVar.gain') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.countVar.loss') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.countVar.net') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="c in r.data.value.counts" :key="c.id" class="border-t border-border" :data-testid="`count-${c.number}`">
              <td class="py-1.5 pr-2">{{ formatDate(c.approved_on) }}</td>
              <td class="py-1.5 pr-2"><RouterLink :to="`/inventory/counts/${c.id}`" class="underline">{{ c.number }}</RouterLink><span class="block text-xs text-muted-foreground">{{ t('inventory.reports.countVar.linesWith', { n: c.lines_with_variance, total: c.lines }) }}</span></td>
              <td class="py-1.5 pr-2">{{ c.warehouse_code }}</td><td class="py-1.5 pr-2">{{ c.approver_name }}</td>
              <td class="py-1.5 pr-2 text-right">{{ formatMoney(c.gain) }}</td><td class="py-1.5 pr-2 text-right">{{ formatMoney(c.loss) }}</td><td class="py-1.5 text-right font-semibold">{{ signed(c.net) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else-if="view === 'LINES'" class="overflow-x-auto" data-testid="countvar-lines">
        <table class="w-full min-w-[48rem] text-sm">
          <thead><tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.countVar.count') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.countVar.book') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.countVar.found') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.countVar.difference') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.value.value') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="(l, n) in r.data.value.lines" :key="n" class="border-t border-border">
              <td class="py-1.5 pr-2">{{ l.item_name }}<span class="block text-xs text-muted-foreground">{{ l.item_code }} · {{ l.category_name }}</span></td>
              <td class="py-1.5 pr-2">{{ l.count_number }}<span class="block text-xs text-muted-foreground">{{ formatDate(l.approved_on) }} · {{ l.warehouse_code }}</span></td>
              <td class="py-1.5 pr-2 text-right">{{ formatQty(l.book_qty) }} {{ l.unit_code }}</td><td class="py-1.5 pr-2 text-right">{{ formatQty(l.counted_qty) }}</td>
              <td class="py-1.5 pr-2 text-right" :class="Number(l.variance_qty) < 0 ? 'text-destructive' : ''">{{ Number(l.variance_qty) > 0 ? '+' : '' }}{{ formatQty(l.variance_qty) }}</td><td class="py-1.5 text-right">{{ signed(l.variance_value) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="overflow-x-auto" data-testid="countvar-items">
        <table class="w-full min-w-[40rem] text-sm">
          <thead><tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.filter.warehouse') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.countVar.counts') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.countVar.difference') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.value.value') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="x in r.data.value.items" :key="`${x.warehouse_id}-${x.item_id}`" class="border-t border-border">
              <td class="py-1.5 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }}</span></td><td class="py-1.5 pr-2">{{ x.warehouse_code }}</td><td class="py-1.5 pr-2 text-right">{{ x.counts }}</td>
              <td class="py-1.5 pr-2 text-right">{{ Number(x.variance_qty) > 0 ? '+' : '' }}{{ formatQty(x.variance_qty) }} {{ x.unit_code }}</td><td class="py-1.5 text-right">{{ signed(x.variance_value) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </template>
</template>
