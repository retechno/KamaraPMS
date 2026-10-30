<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { ZReport } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { formatDate, formatDateTime, formatMoney } from '@/utils/format'
import { money } from '@/views/accounting/reportApi'
import DayReportActions from './DayReportActions.vue'
import { methodName } from './dayLabels'
import { useDayReport } from './useDayReport'

/** The Z report of all drawers (docs/architecture/38-reports-night-audit-pack.md 7.5, mockup 68): every shift of the day, X while it is open, checked against the cashier report. */
const date = ref('')
const { allowed, denied, data, error, busy, run, csv, pdf } = useDayReport<ZReport>('z-reports', () => ({ date: date.value || undefined }), ['report.view', 'cashier.shift_manage'])
watch(() => data.value?.date, (d) => {
  if (d) date.value = d
})
watch(allowed, (ok) => {
  if (ok) void run()
}, { immediate: true })

const report = computed(() => data.value)
const th = 'whitespace-nowrap px-2 py-2 text-[11px] font-medium uppercase text-muted-foreground'
</script>

<template>
  <section class="flex flex-col gap-4" data-testid="z-reports">
    <PageHeader :title="t('dayReports.z.title')" :description="t('dayReports.z.intro')">
      <template #actions>
        <DayReportActions :ready="!!report" :busy="busy" @csv="csv(date)" @pdf="pdf" />
      </template>
    </PageHeader>
    <ErrorNotice :error="error" inline data-testid="report-error" />
    <p v-if="denied" class="muted m-0" data-testid="no-access">{{ t('dayReports.deniedBoth', { first: 'report.view', second: 'cashier.shift_manage' }) }}</p>
    <template v-else>
      <form class="flex flex-wrap items-end gap-3 print:hidden" novalidate @submit.prevent="run">
        <FormField :label="t('dayReports.businessDate')"><template #default="{ id }"><Input :id="id" v-model="date" type="date" name="date" data-testid="z-date" /></template></FormField>
        <Button type="submit" size="sm" :disabled="busy" data-testid="run">{{ t('dayReports.run') }}</Button>
      </form>

      <template v-if="report">
        <EmptyState v-if="!report.blocks.length" :title="t('dayReports.z.noShifts')" :description="t('emptyState.changeSelection')" data-testid="empty" />
        <Card v-else class="overflow-x-auto p-0">
          <table class="w-full border-collapse text-xs tabular-nums" data-testid="z-table">
            <thead>
              <tr class="border-b border-border">
                <th :class="[th, 'text-left']">{{ t('dayReports.z.shift') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.z.period') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.z.float') }}</th>
                <th :class="[th, 'text-right']">{{ t('dayReports.z.cashPayments') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.z.receipts') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.z.payInOut') }}</th>
                <th :class="[th, 'text-right']">{{ t('dayReports.z.drops') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.z.cashRefunds') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.z.expected') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.z.counted') }}</th>
                <th :class="[th, 'text-right']">{{ t('dayReports.z.overShort') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.z.reason') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.z.tenders') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="b in report.blocks" :key="b.shift_id" class="border-b border-border align-top" :data-testid="`z-row-${b.number}`">
                <td class="px-2 py-2"><Badge :variant="b.kind === 'X' ? 'outline' : 'secondary'" :data-testid="`z-kind-${b.number}`">{{ b.kind }}</Badge> {{ b.number }}<div class="text-muted-foreground">{{ b.drawer }} · {{ b.cashier }}</div></td>
                <td class="px-2 py-2">{{ formatDateTime(b.opened_at) }}<div class="text-muted-foreground">{{ b.closed_at ? formatDateTime(b.closed_at) : t('dayReports.z.stillOpen') }}</div></td>
                <td class="px-2 py-2 text-right">{{ money(b.opening_float) }}</td><td class="px-2 py-2 text-right">{{ money(b.cash_payments) }}</td><td class="px-2 py-2 text-right">{{ money(b.cash_receipts) }}</td>
                <td class="px-2 py-2 text-right">{{ money(String(Number(b.pay_ins) + Number(b.pay_outs))) }}</td><td class="px-2 py-2 text-right">{{ money(b.drops) }}</td><td class="px-2 py-2 text-right" :data-testid="`z-refund-${b.number}`">{{ money(b.cash_refunds) }}</td><td class="px-2 py-2 text-right">{{ money(b.expected_cash) }}</td>
                <td class="px-2 py-2 text-right">{{ b.counted_cash === null ? t('dayReports.z.notCounted') : money(b.counted_cash) }}</td>
                <td :class="['px-2 py-2 text-right', b.over_short && Number(b.over_short) !== 0 && 'text-destructive']">{{ b.over_short === null ? '–' : money(b.over_short) }}</td>
                <td class="px-2 py-2">{{ b.variance_reason }}<div v-if="b.approved_by" class="text-muted-foreground">{{ b.approved_by }}</div></td>
                <td class="px-2 py-2"><div v-for="x in b.tenders" :key="`${x.method}-${x.kind}`">{{ methodName(x.method) }}{{ x.kind === 'REFUND' ? ` (${t('dayReports.z.refund')})` : '' }} {{ formatMoney(x.amount) }}</div></td>
              </tr>
            </tbody>
            <tfoot>
              <tr class="font-semibold" data-testid="z-property">
                <td class="px-2 py-2" colspan="2">{{ t('dayReports.z.property') }}</td>
                <td class="px-2 py-2 text-right">{{ money(report.property.opening_float) }}</td><td class="px-2 py-2 text-right">{{ money(report.property.cash_payments) }}</td><td class="px-2 py-2 text-right">{{ money(report.property.cash_receipts) }}</td>
                <td class="px-2 py-2 text-right">{{ money(String(Number(report.property.pay_ins) + Number(report.property.pay_outs))) }}</td><td class="px-2 py-2 text-right">{{ money(report.property.drops) }}</td><td class="px-2 py-2 text-right" data-testid="z-refund-property">{{ money(report.property.cash_refunds) }}</td><td class="px-2 py-2 text-right">{{ money(report.property.expected_cash) }}</td>
                <td class="px-2 py-2 text-right">{{ report.property.counted_cash === null ? '–' : money(report.property.counted_cash) }}</td><td class="px-2 py-2 text-right" data-testid="z-over-short">{{ report.property.over_short === null ? '–' : money(report.property.over_short) }}</td><td colspan="2" />
              </tr>
            </tfoot>
          </table>
        </Card>

        <Card class="p-4" data-testid="z-takings">
          <h2 class="mb-2 mt-0 text-sm font-semibold">
            {{ t('dayReports.z.checked', { date: formatDate(report.date) }) }}
            <Badge :variant="report.reconciled ? 'secondary' : 'destructive'" data-testid="reconciled">{{ report.reconciled ? t('dayReports.matches') : t('dayReports.differs') }}</Badge>
          </h2>
          <table class="w-full border-collapse text-sm tabular-nums">
            <thead>
              <tr class="border-b border-border">
                <th :class="[th, 'text-left']">{{ t('dayReports.z.method') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.z.onShift') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.z.offShift') }}</th>
                <th :class="[th, 'text-right']">{{ t('dayReports.z.total') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.z.cashierReport') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.difference') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="x in report.takings" :key="x.method" class="border-b border-border" :data-testid="`z-taking-${x.method}`">
                <td class="px-2 py-1.5">{{ methodName(x.method) }}</td><td class="px-2 py-1.5 text-right">{{ money(x.on_shift) }}</td><td class="px-2 py-1.5 text-right">{{ money(x.off_shift) }}</td>
                <td class="px-2 py-1.5 text-right">{{ money(x.total) }}</td><td class="px-2 py-1.5 text-right">{{ money(x.cashier_report) }}</td>
                <td :class="['px-2 py-1.5 text-right', Number(x.difference) !== 0 && 'text-destructive']">{{ money(x.difference) }}</td>
              </tr>
            </tbody>
          </table>
          <ul class="mb-0 mt-3 flex list-none flex-col gap-1 p-0 text-sm">
            <li class="flex justify-between gap-3" data-testid="z-off-shift"><span>{{ t('dayReports.z.cashOffShift') }}</span><span class="tabular-nums">{{ money(report.cash_off_shift) }}</span></li>
            <li class="flex justify-between gap-3" data-testid="z-voided"><span>{{ t('dayReports.z.voidedAfterClose') }}</span><span class="tabular-nums">{{ money(report.voided_after_close) }}</span></li>
          </ul>
          <p class="mb-0 mt-3 text-xs text-muted-foreground" data-testid="z-formula">{{ t('dayReports.z.formula') }}</p>
          <p v-if="report.blocks.some((b) => b.kind === 'X')" class="mb-0 mt-3 text-xs text-muted-foreground" data-testid="z-open-note">{{ t('dayReports.z.openNote') }}</p>
        </Card>
      </template>
    </template>
  </section>
</template>
