<script setup lang="ts">
import { watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import type { BanquetGuestsReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { formatDateShort } from '@/utils/format'

/**
 * Guaranteed against actual guests (docs/architecture/36-banquet.md 4.10, mockup 50): for each session of a definite or completed event the guests estimated, guaranteed and that came, with the difference.
 * A positive difference is guests served beyond the guarantee; a negative one is guests the client paid for and who did not come.
 */
const r = useInventoryReport<BanquetGuestsReport>('/api/v1/properties/{propertyId}/banquet/reports/guests', 'banquet-guests.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/reports/guests', { params: { path: { propertyId }, query: { from: p.from, to: p.to } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const signed = (n: number | null): string => (n === null ? '–' : n > 0 ? `+${n}` : String(n))
</script>

<template>
  <PageHeader :title="t('banquet.reports.guests.title')" :description="t('banquet.reports.guests.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv()" />
    <p v-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="guests-empty">{{ t('banquet.reports.empty') }}</p>
    <div v-else-if="r.data.value" class="overflow-x-auto">
      <table class="w-full min-w-[44rem] text-sm" data-testid="guests-table">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('banquet.reports.date') }}</th><th class="py-1 pr-2 font-medium">{{ t('banquet.reports.event') }}</th><th class="py-1 pr-2 font-medium">{{ t('banquet.reports.rooms.room') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.guests.estimated') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.guests.guaranteed') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.guests.actual') }}</th><th class="py-1 text-right font-medium">{{ t('banquet.reports.guests.difference') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="x in r.data.value.rows" :key="x.session_id" class="border-t border-border" :data-testid="`guests-row-${x.session_id}`">
            <td class="py-1.5 pr-2">{{ formatDateShort(x.session_date) }}</td>
            <td class="py-1.5 pr-2"><RouterLink :to="`/banquet/events/${x.event_id}`" class="font-medium text-primary hover:underline">{{ x.event_name }}</RouterLink><span class="block text-xs text-muted-foreground">{{ x.event_number }} · {{ x.session_name }}</span></td>
            <td class="py-1.5 pr-2">{{ x.room_name ?? '–' }}</td>
            <td class="py-1.5 pr-2 text-right text-muted-foreground">{{ x.pax_estimated }}</td><td class="py-1.5 pr-2 text-right">{{ x.pax_guaranteed ?? '–' }}</td><td class="py-1.5 pr-2 text-right">{{ x.pax_actual ?? '–' }}</td>
            <td class="py-1.5 text-right font-medium" :class="x.difference !== null && x.difference < 0 ? 'text-destructive' : ''">{{ signed(x.difference) }}</td>
          </tr>
        </tbody>
        <tfoot>
          <tr class="border-t border-border font-semibold" data-testid="guests-total">
            <td class="py-1.5 pr-2" colspan="4">{{ t('banquet.reports.guests.total', { n: r.data.value.compared_count }) }}</td>
            <td class="py-1.5 pr-2 text-right">{{ r.data.value.guaranteed }}</td><td class="py-1.5 pr-2 text-right">{{ r.data.value.actual }}</td><td class="py-1.5 text-right">{{ signed(r.data.value.difference) }}</td>
          </tr>
        </tfoot>
      </table>
      <p class="mt-3 text-xs text-muted-foreground">{{ t('banquet.reports.guests.note', { over: r.data.value.over, under: r.data.value.under }) }}</p>
    </div>
  </template>
</template>
