<script setup lang="ts">
import { watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import type { BanquetLostReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { Badge } from '@/components/ui/badge'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { formatDateShort, formatMoney } from '@/utils/format'

/**
 * Lost and cancelled events with the reasons given (docs/architecture/36-banquet.md 4.10, mockup 50): how many events, and how much contract value, each reason cost. A lost event never became definite; a
 * cancelled one did.
 */
const r = useInventoryReport<BanquetLostReport>('/api/v1/properties/{propertyId}/banquet/reports/lost', 'banquet-lost.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/reports/lost', { params: { path: { propertyId }, query: { from: p.from, to: p.to } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const money = (v: string): string => (Number(v) === 0 ? '–' : formatMoney(v))
const statusText = (s: 'LOST' | 'CANCELLED'): string => t(s === 'LOST' ? 'banquet.reports.lost.statusLost' : 'banquet.reports.lost.statusCancelled')
</script>

<template>
  <PageHeader :title="t('banquet.reports.lost.title')" :description="t('banquet.reports.lost.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv()" />
    <p v-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="lost-empty">{{ t('banquet.reports.empty') }}</p>
    <template v-else-if="r.data.value">
      <section class="mb-6" data-testid="lost-groups">
        <h2 class="mb-2 text-base font-semibold">{{ t('banquet.reports.lost.byReason') }}</h2>
        <table class="w-full max-w-2xl text-sm">
          <thead><tr class="text-left text-xs text-muted-foreground"><th class="py-1 pr-2 font-medium">{{ t('banquet.reports.lost.reason') }}</th><th class="py-1 pr-2 font-medium" /><th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.forecast.events') }}</th><th class="py-1 text-right font-medium">{{ t('banquet.reports.lost.value') }}</th></tr></thead>
          <tbody>
            <tr v-for="g in r.data.value.groups" :key="`${g.status}/${g.reason_code}`" class="border-t border-border" :data-testid="`lost-group-${g.status}-${g.reason_code || 'none'}`">
              <td class="py-1.5 pr-2 font-medium">{{ g.reason_name || t('banquet.reports.lost.noReason') }}</td><td class="py-1.5 pr-2"><Badge :variant="g.status === 'LOST' ? 'outline' : 'destructive'">{{ statusText(g.status) }}</Badge></td>
              <td class="py-1.5 pr-2 text-right">{{ g.events }}</td><td class="py-1.5 text-right">{{ money(g.value) }}</td>
            </tr>
          </tbody>
        </table>
      </section>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[40rem] text-sm" data-testid="lost-table">
          <thead><tr class="text-left text-xs text-muted-foreground"><th class="py-1 pr-2 font-medium">{{ t('banquet.reports.lost.closedOn') }}</th><th class="py-1 pr-2 font-medium">{{ t('banquet.reports.event') }}</th><th class="py-1 pr-2 font-medium">{{ t('banquet.reports.lost.reason') }}</th><th class="py-1 pr-2 font-medium">{{ t('banquet.reports.sales.person') }}</th><th class="py-1 text-right font-medium">{{ t('banquet.reports.lost.value') }}</th></tr></thead>
          <tbody>
            <tr v-for="x in r.data.value.rows" :key="x.event_id" class="border-t border-border align-top" :data-testid="`lost-row-${x.event_id}`">
              <td class="py-1.5 pr-2">{{ formatDateShort(x.closed_on) }}</td>
              <td class="py-1.5 pr-2"><RouterLink :to="`/banquet/events/${x.event_id}`" class="font-medium text-primary hover:underline">{{ x.event_name }}</RouterLink><span class="block text-xs text-muted-foreground">{{ x.event_number }} · {{ statusText(x.status) }}</span></td>
              <td class="py-1.5 pr-2">{{ x.reason_name || '–' }}<span v-if="x.reason_note" class="block text-xs text-muted-foreground">{{ x.reason_note }}</span></td>
              <td class="py-1.5 pr-2">{{ x.sales_name }}</td><td class="py-1.5 text-right">{{ money(x.value) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </template>
</template>
