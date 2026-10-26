<script setup lang="ts">
import { watch } from 'vue'
import { api } from '@/api/client'
import type { BanquetRoomsReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { formatMoney, formatPercent } from '@/utils/format'

/**
 * Revenue and occupancy by room (docs/architecture/36-banquet.md 4.10, mockup 50): the hours a room was held by definite and completed sessions against the hours of operation x the days, and the net revenue of
 * the sessions posted in the period. A combined room and its parts block each other, so their hours are counted once.
 */
const r = useInventoryReport<BanquetRoomsReport>('/api/v1/properties/{propertyId}/banquet/reports/rooms', 'banquet-rooms.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/reports/rooms', { params: { path: { propertyId }, query: { from: p.from, to: p.to } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const money = (v: string | null | undefined): string => (v === null || v === undefined || Number(v) === 0 ? '–' : formatMoney(v))
</script>

<template>
  <PageHeader :title="t('banquet.reports.rooms.title')" :description="t('banquet.reports.rooms.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv()" />
    <p v-if="r.loaded.value && !r.data.value?.rooms.length" class="m-0 text-sm text-muted-foreground" data-testid="rooms-empty">{{ t('banquet.reports.empty') }}</p>
    <div v-else-if="r.data.value" class="overflow-x-auto">
      <p class="mb-2 text-xs text-muted-foreground" data-testid="rooms-hours">{{ t('banquet.reports.rooms.hours', { from: r.data.value.operating_from, to: r.data.value.operating_to, days: r.data.value.days }) }}</p>
      <table class="w-full min-w-[44rem] text-sm" data-testid="rooms-table">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('banquet.reports.rooms.room') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.rooms.sessions') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.rooms.held') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.rooms.available') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.rooms.occupancy') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.rooms.posted') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.rooms.revenue') }}</th><th class="py-1 text-right font-medium">{{ t('banquet.reports.rooms.perHour') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="x in r.data.value.rooms" :key="x.room_id" class="border-t border-border" :data-testid="`room-${x.code}`">
            <td class="py-1.5 pr-2 font-medium">{{ x.name }}<span v-if="x.is_combo" class="ml-1 text-xs font-normal text-muted-foreground">{{ t('banquet.reports.rooms.combo') }}</span></td>
            <td class="py-1.5 pr-2 text-right">{{ x.sessions }}</td><td class="py-1.5 pr-2 text-right">{{ x.hours }}</td><td class="py-1.5 pr-2 text-right text-muted-foreground">{{ x.available_hours }}</td>
            <td class="py-1.5 pr-2 text-right">{{ x.occupancy_pct === null ? '–' : formatPercent(x.occupancy_pct) }}</td><td class="py-1.5 pr-2 text-right">{{ x.posted_sessions }}</td>
            <td class="py-1.5 pr-2 text-right">{{ money(x.revenue) }}</td><td class="py-1.5 text-right text-muted-foreground">{{ money(x.revenue_per_hour) }}</td>
          </tr>
        </tbody>
        <tfoot><tr class="border-t border-border font-semibold"><td class="py-1.5 pr-2" colspan="6">{{ t('banquet.reports.total') }}</td><td class="py-1.5 pr-2 text-right" data-testid="rooms-total">{{ money(r.data.value.revenue) }}</td><td /></tr></tfoot>
      </table>
      <p class="mt-3 text-xs text-muted-foreground">{{ t('banquet.reports.rooms.note') }}</p>
    </div>
  </template>
</template>
