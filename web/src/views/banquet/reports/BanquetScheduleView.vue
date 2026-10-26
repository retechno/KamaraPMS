<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetScheduleReport, BanquetScheduleRow } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { downloadCsv } from '@/views/accounting/reportApi'

/**
 * The schedule of a day (docs/architecture/36-banquet.md 4.10, mockup 50): every session of the day by its start time, with the room, the set-up, the guests, the contact and the state of the BEO, made to be
 * printed and put on the board of the kitchen and of the banquet office.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const day = ref('')
const data = ref<BanquetScheduleReport | null>(null)
const error = ref<ApiError | null>(null)
const loaded = ref(false)
const busy = ref(false)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) return
  if (!day.value) day.value = property.clock?.business_date ?? new Date().toISOString().slice(0, 10)
  busy.value = true
  try {
    const res = await api.GET('/api/v1/properties/{propertyId}/banquet/reports/schedule', { params: { path: { propertyId }, query: { day: day.value } } })
    data.value = res.data ?? null
  } catch (e) {
    data.value = null
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

async function csv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    await downloadCsv('/api/v1/properties/{propertyId}/banquet/reports/schedule', { path: { propertyId }, query: { day: day.value } }, `banquet-schedule-${day.value}.csv`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function print(): void {
  window.print()
}
const hours = (s: BanquetScheduleRow): string => (s.start_time && s.end_time ? `${s.start_time} – ${s.end_time}${s.ends_next_day ? ' +1' : ''}` : '–')
const guests = (s: BanquetScheduleRow): string => String(s.pax_actual ?? s.pax_guaranteed ?? s.pax_estimated)
</script>

<template>
  <PageHeader :title="t('banquet.reports.schedule.title')" :description="t('banquet.reports.schedule.intro')" />
  <ReportPrintHeader :to="day" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <form class="mb-4 flex flex-wrap items-end gap-3 print:hidden" novalidate data-testid="report-period" @submit.prevent="load">
      <FormField :label="t('banquet.reports.schedule.day')"><template #default="{ id }"><Input :id="id" v-model="day" type="date" name="day" class="w-40" /></template></FormField>
      <Button type="submit" variant="outline" :disabled="busy" data-testid="report-show">{{ t('inventory.reports.show') }}</Button>
      <div class="ml-auto flex flex-wrap gap-2">
        <Button type="button" variant="outline" data-testid="report-print" @click="print">{{ t('inventory.reports.print') }}</Button>
        <Button type="button" variant="outline" :disabled="busy" data-testid="report-csv" @click="csv">{{ t('inventory.reports.csv') }}</Button>
      </div>
    </form>
    <p v-if="loaded && !data?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="schedule-empty">{{ t('banquet.reports.schedule.none') }}</p>
    <div v-else-if="data" class="overflow-x-auto">
      <table class="w-full min-w-[48rem] text-sm" data-testid="schedule-table">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('banquet.reports.schedule.time') }}</th><th class="py-1 pr-2 font-medium">{{ t('banquet.reports.rooms.room') }}</th><th class="py-1 pr-2 font-medium">{{ t('banquet.reports.event') }}</th>
            <th class="py-1 pr-2 font-medium">{{ t('banquet.reports.schedule.setup') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.schedule.guests') }}</th>
            <th class="py-1 pr-2 font-medium">{{ t('banquet.reports.schedule.contact') }}</th><th class="py-1 font-medium">{{ t('banquet.reports.schedule.beo') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="s in data.rows" :key="s.session_id" class="border-t border-border align-top" :data-testid="`schedule-row-${s.session_id}`">
            <td class="py-1.5 pr-2 font-medium whitespace-nowrap">{{ hours(s) }}</td><td class="py-1.5 pr-2">{{ s.room_name ?? '–' }}</td>
            <td class="py-1.5 pr-2"><RouterLink :to="`/banquet/events/${s.event_id}`" class="font-medium text-primary hover:underline print:text-foreground print:no-underline">{{ s.event_name }}</RouterLink><span class="block text-xs text-muted-foreground">{{ s.event_number }} · {{ s.session_name }}</span></td>
            <td class="py-1.5 pr-2">{{ s.setup_style ?? '–' }}</td>
            <td class="py-1.5 pr-2 text-right">{{ guests(s) }}<span v-if="s.pax_guaranteed !== null" class="block text-xs text-muted-foreground">{{ t('banquet.reports.guests.guaranteed') }} {{ s.pax_guaranteed }}</span></td>
            <td class="py-1.5 pr-2">{{ s.contact_name ?? '–' }}<span v-if="s.contact_phone" class="block text-xs text-muted-foreground">{{ s.contact_phone }}</span></td>
            <td class="py-1.5"><Badge v-if="s.beo_revision === 0" variant="outline">{{ t('banquet.reports.schedule.beoNone') }}</Badge><Badge v-else :variant="s.beo_unread > 0 ? 'destructive' : 'success'">{{ s.beo_unread > 0 ? t('banquet.reports.schedule.beoUnread', { rev: s.beo_revision, n: s.beo_unread }) : t('banquet.reports.schedule.beoRev', { rev: s.beo_revision }) }}</Badge></td>
          </tr>
        </tbody>
      </table>
    </div>
  </template>
</template>
