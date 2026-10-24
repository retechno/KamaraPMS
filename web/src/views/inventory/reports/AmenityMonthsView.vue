<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryAmenityMonthsReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatMoney, formatPercent } from '@/utils/format'
import { formatQty } from '@/utils/inventory'

/** The room amenities by month (docs/architecture/35-inventory.md 12.5, 22): occupied rooms, guests, the cost, the cost per occupied room, per month and per item, against the month before. The period opens on the last six months. */
const property = usePropertyStore()
const r = useInventoryReport<InventoryAmenityMonthsReport>('/api/v1/properties/{propertyId}/inventory/reports/amenities-by-month', 'amenities-by-month.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/amenities-by-month', { params: { path: { propertyId }, query: { from: p.from, to: p.to } } })
  return data ?? null
})
// the six months up to the month of the business date
if (!r.period.from) {
  const bd = property.clock?.business_date ?? new Date().toISOString().slice(0, 10)
  const d = new Date(Date.UTC(Number(bd.slice(0, 4)), Number(bd.slice(5, 7)) - 1 - 5, 1))
  r.period.to = bd
  r.period.from = d.toISOString().slice(0, 10)
}
watch(r.pid, r.load, { immediate: true })
const open = ref<Record<string, boolean>>({})
const monthLabel = (m: string): string => new Date(`${m.slice(0, 7)}-01T00:00:00Z`).toLocaleDateString(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' })
const money = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : formatMoney(v))
const change = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : `${Number(v) > 0 ? '+' : ''}${formatPercent(v)}`)
</script>

<template>
  <PageHeader :title="t('inventory.reports.amenityMonths.title')" :description="t('inventory.reports.amenityMonths.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv()" />
    <p v-if="r.loaded.value && !r.data.value?.months.length" class="m-0 text-sm text-muted-foreground" data-testid="amenity-empty">{{ t('inventory.reports.empty') }}</p>
    <div v-else-if="r.data.value" class="flex flex-col gap-3">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[56rem] text-sm" data-testid="amenity-table">
          <thead><tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.amenityMonths.month') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.amenityMonths.rooms') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.amenityMonths.guests') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.amenityMonths.cost') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.amenityMonths.perRoom') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.amenityMonths.prev') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.amenityMonths.change') }}</th>
          </tr></thead>
          <tbody>
            <template v-for="m in r.data.value.months" :key="m.month">
              <tr class="border-t border-border" :data-testid="`month-${m.month.slice(0, 7)}`">
                <td class="py-1.5 pr-2">
                  <button type="button" class="underline print:no-underline" :aria-expanded="!!open[m.month]" :data-testid="`month-toggle-${m.month.slice(0, 7)}`" @click="open[m.month] = !open[m.month]">{{ monthLabel(m.month) }}</button>
                </td>
                <td class="py-1.5 pr-2 text-right">{{ m.occupied_rooms }}</td><td class="py-1.5 pr-2 text-right">{{ m.guests }}</td>
                <td class="py-1.5 pr-2 text-right font-semibold">{{ formatMoney(m.cost) }}</td><td class="py-1.5 pr-2 text-right">{{ money(m.cost_per_room) }}</td>
                <td class="py-1.5 pr-2 text-right">{{ money(m.prev_cost) }}</td><td class="py-1.5 text-right">{{ change(m.change_pct) }}</td>
              </tr>
              <tr v-if="open[m.month]" class="border-t border-border bg-muted/30">
                <td colspan="7" class="p-2">
                  <table class="w-full text-sm">
                    <thead><tr class="text-left text-xs text-muted-foreground"><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.usage.qty') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.amenityMonths.cost') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.amenityMonths.prev') }}</th></tr></thead>
                    <tbody>
                      <tr v-for="x in m.items" :key="x.item_id" class="border-t border-border">
                        <td class="py-1 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }}</span></td>
                        <td class="py-1 pr-2 text-right">{{ formatQty(x.qty) }} {{ x.unit_code }}</td><td class="py-1 pr-2 text-right">{{ formatMoney(x.cost) }}</td><td class="py-1 text-right">{{ money(x.prev_cost) }}</td>
                      </tr>
                    </tbody>
                  </table>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </div>
  </template>
</template>
