<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import type { CancellationGroup, CancellationReport } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { DateInput } from '@/components/ui/date-input'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { addDays } from '@/utils/dates'
import { formatDate, formatDateShort, formatDateTime, formatMoney } from '@/utils/format'
import { money } from '@/views/accounting/reportApi'
import { guardForm } from '@/utils/forms'
import DayReportActions from './DayReportActions.vue'
import { sourceName } from './dayLabels'
import { useDayReport } from './useDayReport'

/** The cancellations and no-shows (docs/architecture/38-reports-night-audit-pack.md 7.2, mockup 64): one row for each event, and the fee only when it was posted. */
const f = reactive({ from: '', to: '', kind: 'ALL', source: '' })
const { businessDate, allowed, denied, data, error, busy, loaded, run, csv, pdf } = useDayReport<CancellationReport>('cancellations', () => ({ from: f.from, to: f.to, kind: f.kind === 'ALL' ? undefined : f.kind, source: f.source.trim() || undefined }))
watch(businessDate, (bd) => {
  if (!bd) return
  f.to ||= bd
  f.from ||= addDays(bd, -6)
}, { immediate: true })
watch([allowed, businessDate], ([ok, bd]) => {
  if (ok && bd && !loaded.value) void run()
}, { immediate: true })

const report = computed(() => data.value)
const nights = computed(() => (report.value?.events ?? []).reduce((n, e) => n + (e.nights ?? 0) * e.rooms, 0))
const valueLost = computed(() => (report.value?.by_kind ?? []).reduce((s, g) => s + Number(g.value), 0))
const withFee = computed(() => (report.value?.events ?? []).filter((e) => e.fee_net !== null).length)
/** The name of a group: the reason, the source (named, not its code) or the company; the empty key says what is missing. */
const groupKey = (g: 'by_reason' | 'by_source' | 'by_company', key: string): string => {
  if (g === 'by_source') return key ? sourceName(key) : t('dayReports.cancellations.noSource')
  if (g === 'by_company') return key || t('dayReports.cancellations.noCompany')
  return key || t('dayReports.cancellations.noReason')
}
const held = (g: CancellationGroup): string => `${g.events} · ${money(g.value)}`
const th = 'whitespace-nowrap px-2 py-2 text-left text-[11px] font-medium uppercase text-muted-foreground'
</script>

<template>
  <section class="flex flex-col gap-4" data-testid="cancellations">
    <PageHeader :title="t('dayReports.cancellations.title')" :description="t('dayReports.cancellations.intro')">
      <template #actions>
        <DayReportActions :ready="!!report" :busy="busy" @csv="csv(`${f.from}_${f.to}`)" @pdf="pdf" />
      </template>
    </PageHeader>
    <ErrorNotice :error="error" inline data-testid="report-error" />
    <p v-if="denied" class="muted m-0" data-testid="no-access">{{ t('dayReports.denied', { permission: 'report.view' }) }}</p>
    <template v-else>
      <form class="flex flex-wrap items-end gap-3 print:hidden" novalidate @submit.prevent="guardForm($event) && run()">
        <FormField :label="t('dayReports.from')"><template #default="{ id }"><DateInput :id="id" v-model="f.from" name="from" data-testid="cx-from" /></template></FormField>
        <FormField :label="t('dayReports.to')"><template #default="{ id }"><DateInput :id="id" v-model="f.to" name="to" data-testid="cx-to" /></template></FormField>
        <FormField :label="t('dayReports.cancellations.kindLabel')"><template #default="{ id }">
          <NativeSelect :id="id" v-model="f.kind" name="kind" data-testid="cx-kind">
            <option value="ALL">{{ t('dayReports.all') }}</option>
            <option value="CANCELLED">{{ t('dayReports.cancellations.kind.CANCELLED') }}</option>
            <option value="NO_SHOW">{{ t('dayReports.cancellations.kind.NO_SHOW') }}</option>
          </NativeSelect>
        </template></FormField>
        <FormField :label="t('dayReports.cancellations.source')"><template #default="{ id }"><Input :id="id" v-model="f.source" name="source" data-testid="cx-source" /></template></FormField>
        <Button type="submit" size="sm" :disabled="busy" data-testid="run">{{ t('dayReports.run') }}</Button>
      </form>

      <template v-if="report">
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" data-testid="cx-summary">
          <Card class="p-4"><div class="text-xs uppercase text-muted-foreground">{{ t('dayReports.cancellations.events') }}</div><div class="text-xl font-semibold tabular-nums" data-testid="cx-events">{{ report.events.length }}</div></Card>
          <Card class="p-4"><div class="text-xs uppercase text-muted-foreground">{{ t('dayReports.cancellations.nightsLost') }}</div><div class="text-xl font-semibold tabular-nums" data-testid="cx-nights">{{ nights }}</div><div class="text-xs text-muted-foreground">{{ t('dayReports.cancellations.valueLost') }} {{ formatMoney(String(valueLost)) }}</div></Card>
          <Card class="p-4"><div class="text-xs uppercase text-muted-foreground">{{ t('dayReports.cancellations.feesPosted') }}</div><div class="text-xl font-semibold tabular-nums" data-testid="cx-fees">{{ formatMoney(report.fees_posted) }}</div><div class="text-xs text-muted-foreground">{{ t('dayReports.cancellations.feeCount', { n: withFee, total: report.events.length }) }}</div></Card>
          <Card class="p-4"><div class="text-xs uppercase text-muted-foreground">{{ t('dayReports.cancellations.depositsHeld') }}</div><div class="text-xl font-semibold tabular-nums" data-testid="cx-deposits">{{ formatMoney(report.deposits_held) }}</div></Card>
        </div>

        <EmptyState v-if="!report.events.length" :title="t('dayReports.empty')" :description="t('emptyState.changeSelection')" data-testid="empty" />
        <Card v-else class="overflow-x-auto p-0">
          <table class="w-full border-collapse text-xs tabular-nums" data-testid="cx-table">
            <thead>
              <tr class="border-b border-border">
                <th :class="th">{{ t('dayReports.cancellations.when') }}</th><th :class="th">{{ t('dayReports.cancellations.kindLabel') }}</th><th :class="th">{{ t('dayReports.cancellations.reservation') }}</th>
                <th :class="th">{{ t('dayReports.cancellations.guest') }}</th><th :class="th">{{ t('dayReports.cancellations.source') }}</th><th :class="th">{{ t('dayReports.cancellations.room') }}</th>
                <th :class="th">{{ t('dayReports.cancellations.nights') }}</th><th :class="th">{{ t('dayReports.cancellations.lead') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.cancellations.value') }}</th>
                <th :class="th">{{ t('dayReports.cancellations.reason') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.cancellations.fee') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.cancellations.deposit') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(e, i) in report.events" :key="i" class="border-b border-border align-top" :data-testid="`cx-row-${e.confirmation_number}-${i}`">
                <td class="px-2 py-2">{{ formatDateTime(e.event_at) }}<div class="text-muted-foreground">{{ e.user_name }}</div></td>
                <td class="px-2 py-2"><Badge :variant="e.kind === 'NO_SHOW' ? 'destructive' : 'secondary'">{{ t(`dayReports.cancellations.kind.${e.kind}` as 'dayReports.cancellations.kind.NO_SHOW') }}</Badge><Badge v-if="e.reinstated" variant="outline" class="ml-1" data-testid="cx-reinstated">{{ t('dayReports.cancellations.reinstated') }}</Badge></td>
                <td class="px-2 py-2">{{ e.confirmation_number }}</td>
                <td class="px-2 py-2">{{ e.guest }}<div v-if="e.company" class="text-muted-foreground">{{ e.company }}</div></td>
                <td class="px-2 py-2">{{ e.source ? sourceName(e.source) : t('dayReports.cancellations.noSource') }}</td>
                <td class="px-2 py-2">{{ e.rooms > 1 ? `${e.rooms} × ` : '' }}{{ e.room_type }}<div v-if="e.arrival" class="text-muted-foreground">{{ formatDateShort(e.arrival) }} – {{ formatDate(e.departure) }}</div></td>
                <td class="px-2 py-2">{{ e.nights ?? '–' }}</td>
                <td class="px-2 py-2">{{ e.lead_days ?? '–' }}</td>
                <td class="px-2 py-2 text-right">{{ e.value_lost === null ? '–' : formatMoney(e.value_lost) }}</td>
                <td class="px-2 py-2">{{ e.reason || t('dayReports.cancellations.noReason') }}</td>
                <td class="px-2 py-2 text-right" :data-testid="`cx-fee-${i}`">{{ e.fee_net === null ? t('dayReports.cancellations.notPosted') : formatMoney(e.fee_gross ?? e.fee_net) }}</td>
                <td class="px-2 py-2 text-right">{{ money(e.deposit_now) }}</td>
              </tr>
            </tbody>
          </table>
        </Card>

        <div class="grid gap-3 md:grid-cols-3" data-testid="cx-groups">
          <Card v-for="g in (['by_reason', 'by_source', 'by_company'] as const)" :key="g" class="p-4" :data-testid="`cx-${g}`">
            <h2 class="mb-2 mt-0 text-sm font-semibold">{{ t(`dayReports.cancellations.${g}` as 'dayReports.cancellations.by_reason') }}</h2>
            <ul class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
              <li v-for="x in report[g]" :key="x.key" class="flex justify-between gap-3"><span>{{ groupKey(g, x.key) }}</span><span class="tabular-nums">{{ held(x) }}</span></li>
            </ul>
          </Card>
        </div>
      </template>
    </template>
  </section>
</template>
