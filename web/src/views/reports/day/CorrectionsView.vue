<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import type { CorrectionReport, CorrectionRow } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { DateInput } from '@/components/ui/date-input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { addDays } from '@/utils/dates'
import { formatDateShort, formatDateTime, formatMoney, formatPercent } from '@/utils/format'
import { money } from '@/views/accounting/reportApi'
import { guardForm } from '@/utils/forms'
import DayReportActions from './DayReportActions.vue'
import { methodName } from './dayLabels'
import { useDayReport } from './useDayReport'

/** Voids, corrections, discounts and allowances (docs/architecture/38-reports-night-audit-pack.md 7.4, mockup 67), folio and POS, with who did it and who approved it. */
const KINDS = ['VOID', 'ADJUSTMENT', 'ALLOWANCE', 'REFUND', 'DISCOUNT', 'COMP', 'POS_VOID'] as const
const f = reactive({ from: '', to: '', kind: 'ALL' })
const { businessDate, allowed, denied, data, error, busy, loaded, run, csv, pdf } = useDayReport<CorrectionReport>('corrections', () => ({ from: f.from, to: f.to, kind: f.kind === 'ALL' ? undefined : f.kind }))
watch(businessDate, (bd) => {
  if (!bd) return
  f.to ||= bd
  f.from ||= addDays(bd, -6)
}, { immediate: true })
watch([allowed, businessDate], ([ok, bd]) => {
  if (ok && bd && !loaded.value) void run()
}, { immediate: true })

const report = computed(() => data.value)
const kindName = (k: string): string => t(`dayReports.corrections.kind.${k}` as 'dayReports.corrections.kind.VOID')
/** A void of a payment is another thing than a void of a charge: money went back, no revenue was reduced. */
const isPayment = (r: CorrectionRow): boolean => r.payment_method !== ''
const rowKind = (r: CorrectionRow): string => (r.kind === 'VOID' && isPayment(r) ? 'PAYMENT_VOID' : r.kind)
/** What was corrected: the name of the charge, or the method of the payment; never the code. */
const item = (r: CorrectionRow): string => (isPayment(r) ? methodName(r.payment_method) : r.charge_code_name)
const figure = (r: CorrectionRow, v: string): string => (isPayment(r) ? '–' : money(v))
/** The reference of a row: the folio, or the bill of the POS. */
const reference = (r: CorrectionRow): string => r.folio || r.bill
const th = 'whitespace-nowrap px-2 py-2 text-[11px] font-medium uppercase text-muted-foreground'
</script>

<template>
  <section class="flex flex-col gap-4" data-testid="corrections">
    <PageHeader :title="t('dayReports.corrections.title')" :description="t('dayReports.corrections.intro')">
      <template #actions>
        <DayReportActions :ready="!!report" :busy="busy" @csv="csv(`${f.from}_${f.to}`)" @pdf="pdf" />
      </template>
    </PageHeader>
    <ErrorNotice :error="error" inline data-testid="report-error" />
    <p v-if="denied" class="muted m-0" data-testid="no-access">{{ t('dayReports.denied', { permission: 'report.view' }) }}</p>
    <template v-else>
      <form class="mb-5 flex flex-wrap items-end gap-3 print:hidden" novalidate @submit.prevent="guardForm($event) && run()">
        <FormField :label="t('dayReports.from')"><template #default="{ id }"><DateInput :id="id" v-model="f.from" name="from" data-testid="co-from" /></template></FormField>
        <FormField :label="t('dayReports.to')"><template #default="{ id }"><DateInput :id="id" v-model="f.to" name="to" data-testid="co-to" /></template></FormField>
        <FormField :label="t('dayReports.corrections.kindLabel')"><template #default="{ id }">
          <NativeSelect :id="id" v-model="f.kind" name="kind" data-testid="co-kind">
            <option value="ALL">{{ t('dayReports.all') }}</option>
            <option v-for="k in KINDS" :key="k" :value="k">{{ kindName(k) }}</option>
          </NativeSelect>
        </template></FormField>
        <Button type="submit" size="sm" :disabled="busy" data-testid="run">{{ t('dayReports.run') }}</Button>
      </form>

      <template v-if="report">
        <EmptyState v-if="!report.rows.length" :title="t('dayReports.empty')" :description="t('emptyState.changeSelection')" data-testid="empty" />
        <Card v-else class="overflow-x-auto p-0">
          <table class="w-full border-collapse text-xs tabular-nums" data-testid="co-table">
            <thead>
              <tr class="border-b border-border">
                <th :class="[th, 'text-left']">{{ t('dayReports.corrections.when') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.corrections.kindLabel') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.corrections.reference') }}</th>
                <th :class="[th, 'text-left']">{{ t('dayReports.corrections.line') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.corrections.net') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.corrections.service') }}</th>
                <th :class="[th, 'text-right']">{{ t('dayReports.corrections.tax') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.corrections.gross') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.corrections.reason') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.corrections.by') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(r, i) in report.rows" :key="i" class="border-b border-border align-top" :data-testid="`co-row-${i}`">
                <td class="px-2 py-2">{{ formatDateTime(r.at) }}</td>
                <td class="px-2 py-2"><Badge variant="secondary" :data-testid="`co-kind-${i}`">{{ kindName(rowKind(r)) }}</Badge></td>
                <td class="px-2 py-2">{{ reference(r) }}<div v-if="r.outlet" class="text-muted-foreground">{{ r.outlet }}</div><div v-else-if="r.reservation" class="text-muted-foreground">{{ r.reservation }}</div></td>
                <td class="px-2 py-2">{{ r.description }}<div class="text-muted-foreground">{{ item(r) }}<span v-if="r.original_date"> · {{ formatDateShort(r.original_date) }}</span></div></td>
                <td class="px-2 py-2 text-right">{{ figure(r, r.net) }}</td><td class="px-2 py-2 text-right">{{ figure(r, r.service) }}</td><td class="px-2 py-2 text-right">{{ figure(r, r.tax) }}</td><td class="px-2 py-2 text-right">{{ money(r.gross) }}</td>
                <td class="px-2 py-2">{{ r.reason }}</td>
                <td class="px-2 py-2">{{ r.done_by }}<span v-if="r.approved_by" class="text-muted-foreground"> · {{ r.approved_by }}</span></td>
              </tr>
            </tbody>
            <tfoot>
              <tr class="border-b border-border font-semibold" data-testid="co-reduced">
                <td class="px-2 py-2" colspan="4">{{ t('dayReports.corrections.reduced') }} · {{ t('dayReports.corrections.events', { n: report.reduced.count }) }}<div class="font-normal text-muted-foreground">{{ t('dayReports.corrections.reducedHint') }}</div></td>
                <td class="px-2 py-2 text-right">{{ formatMoney(report.reduced.net) }}</td><td class="px-2 py-2 text-right">{{ formatMoney(report.reduced.service) }}</td><td class="px-2 py-2 text-right">{{ formatMoney(report.reduced.tax) }}</td><td class="px-2 py-2 text-right">{{ formatMoney(report.reduced.gross) }}</td><td colspan="2" />
              </tr>
              <tr class="font-semibold" data-testid="co-money-back">
                <td class="px-2 py-2" colspan="4">{{ t('dayReports.corrections.moneyBack') }} · {{ t('dayReports.corrections.events', { n: report.money_back.count }) }}<div class="font-normal text-muted-foreground">{{ t('dayReports.corrections.moneyBackHint') }}</div></td>
                <td class="px-2 py-2 text-right">–</td><td class="px-2 py-2 text-right">–</td><td class="px-2 py-2 text-right">–</td><td class="px-2 py-2 text-right">{{ formatMoney(report.money_back.amount) }}</td><td colspan="2" />
              </tr>
            </tfoot>
          </table>
        </Card>

        <div class="grid gap-3 md:grid-cols-3" data-testid="co-groups">
          <Card v-for="g in (['by_kind', 'by_user', 'by_outlet'] as const)" :key="g" class="p-4" :data-testid="`co-${g}`">
            <h2 class="mb-2 mt-0 text-sm font-semibold">{{ t(`dayReports.corrections.${g}` as 'dayReports.corrections.by_kind') }}</h2>
            <ul class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
              <li v-for="x in report[g]" :key="x.key" class="flex justify-between gap-3"><span>{{ g === 'by_kind' ? kindName(x.key) : g === 'by_outlet' && !x.key ? t('dayReports.corrections.hotelFolios') : x.key || '–' }} · {{ x.count }}</span><span class="tabular-nums">{{ formatMoney(x.net) }}</span></li>
            </ul>
          </Card>
        </div>

        <Card class="p-4" data-testid="co-share">
          <h2 class="mb-2 mt-0 text-sm font-semibold">{{ t('dayReports.corrections.share') }}</h2>
          <ul class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
            <li class="flex justify-between gap-3"><span>{{ t('dayReports.corrections.netRevenue') }}</span><span class="tabular-nums">{{ formatMoney(report.net_revenue) }}</span></li>
            <li class="flex justify-between gap-3"><span>{{ t('dayReports.corrections.takenBack') }}</span><span class="tabular-nums" data-testid="co-taken-back">{{ formatMoney(report.revenue_taken_back) }}</span></li>
            <li class="flex justify-between gap-3 font-semibold"><span>{{ t('dayReports.corrections.portion') }}</span><span class="tabular-nums" data-testid="co-portion">{{ report.share_percent === null ? '–' : formatPercent(report.share_percent) }}</span></li>
          </ul>
        </Card>
      </template>
    </template>
  </section>
</template>
