<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { DepositLedgerReport } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { DateInput } from '@/components/ui/date-input'
import { t } from '@/i18n'
import { formatDate, formatDateShort, formatMoney } from '@/utils/format'
import { money } from '@/views/accounting/reportApi'
import { guardForm } from '@/utils/forms'
import DayReportActions from './DayReportActions.vue'
import LedgerChecks from './LedgerChecks.vue'
import { useDayReport } from './useDayReport'

/** The deposit ledger of a closed business day (docs/architecture/38-reports-night-audit-pack.md 7.3, mockup 66): the deposits held for each folio and what moved on the day. */
const asOf = ref('')
const { allowed, denied, data, error, busy, run, csv, pdf } = useDayReport<DepositLedgerReport>('deposit-ledger', () => ({ as_of: asOf.value || undefined }))
watch(() => data.value?.as_of, (d) => {
  if (d) asOf.value = d
})
watch(allowed, (ok) => {
  if (ok) void run()
}, { immediate: true })

const report = computed(() => data.value)
const th = 'whitespace-nowrap px-2 py-2 text-[11px] font-medium uppercase text-muted-foreground'
</script>

<template>
  <section class="flex flex-col gap-4" data-testid="deposit-ledger">
    <PageHeader :title="t('dayReports.depositLedger.title')" :description="t('dayReports.depositLedger.intro')">
      <template #actions>
        <DayReportActions :ready="!!report" :busy="busy" @csv="csv(asOf)" @pdf="pdf" />
      </template>
    </PageHeader>
    <ErrorNotice :error="error" inline data-testid="report-error" />
    <p v-if="denied" class="muted m-0" data-testid="no-access">{{ t('dayReports.denied', { permission: 'report.view' }) }}</p>
    <template v-else>
      <form class="flex flex-wrap items-end gap-3 print:hidden" novalidate @submit.prevent="guardForm($event) && run()">
        <FormField :label="t('dayReports.asOf')"><template #default="{ id }"><DateInput :id="id" v-model="asOf" name="as_of" data-testid="dl-as-of" /></template></FormField>
        <Button type="submit" size="sm" :disabled="busy" data-testid="run">{{ t('dayReports.run') }}</Button>
        <Button as-child variant="outline" size="sm"><RouterLink to="/reports/guest-ledger" data-testid="to-guest-ledger">{{ t('dayReports.guestLedger.title') }}</RouterLink></Button>
      </form>

      <template v-if="report">
        <p class="m-0 text-sm text-muted-foreground" data-testid="dl-movement">
          {{ t('dayReports.depositLedger.movement', { opening: formatMoney(report.opening), received: formatMoney(report.received), applied: formatMoney(report.applied), refunded: formatMoney(report.refunded), closing: formatMoney(report.closing) }) }}
          <Badge v-if="report.release_mode" variant="outline">{{ t(`dayReports.releaseMode.${report.release_mode}` as 'dayReports.releaseMode.EVERY_DAY') }}</Badge>
        </p>
        <EmptyState v-if="!report.rows.length" :title="t('dayReports.empty')" :description="t('emptyState.changeSelection')" data-testid="empty" />
        <Card v-else class="overflow-x-auto p-0">
          <table class="w-full border-collapse text-xs tabular-nums" data-testid="dl-table">
            <thead>
              <tr class="border-b border-border">
                <th :class="[th, 'text-left']">{{ t('dayReports.guestLedger.folio') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.guestLedger.owner') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.depositLedger.arrival') }}</th>
                <th :class="[th, 'text-right']">{{ t('dayReports.depositLedger.opening') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.depositLedger.received') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.depositLedger.applied') }}</th>
                <th :class="[th, 'text-right']">{{ t('dayReports.depositLedger.refunded') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.depositLedger.closing') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.depositLedger.note') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in report.rows" :key="r.folio_id" class="border-b border-border align-top" :data-testid="`dl-row-${r.folio_number}`">
                <td class="px-2 py-2">{{ r.folio_number }}</td>
                <td class="px-2 py-2">{{ r.owner }}<div v-if="r.confirmation_number" class="text-muted-foreground">{{ r.confirmation_number }}</div></td>
                <td class="px-2 py-2">{{ r.arrival ? formatDateShort(r.arrival) : '' }}</td>
                <td class="px-2 py-2 text-right">{{ money(r.opening) }}</td><td class="px-2 py-2 text-right">{{ money(r.received) }}</td><td class="px-2 py-2 text-right">{{ money(r.applied) }}</td>
                <td class="px-2 py-2 text-right">{{ money(r.refunded) }}</td><td class="px-2 py-2 text-right font-medium">{{ money(r.closing) }}</td>
                <td class="px-2 py-2">
                  <Badge v-if="r.overdue" variant="destructive" :data-testid="`dl-overdue-${r.folio_number}`">{{ t('dayReports.depositLedger.overdue', { date: formatDate(r.expected_use) }) }}</Badge>
                  <Badge v-if="!r.balanced" variant="destructive" :data-testid="`dl-unbalanced-${r.folio_number}`">{{ t('dayReports.depositLedger.unbalanced') }}</Badge>
                </td>
              </tr>
            </tbody>
            <tfoot>
              <tr class="font-semibold" data-testid="dl-total">
                <td class="px-2 py-2" colspan="3">{{ t('dayReports.total') }} · {{ t('dayReports.guestLedger.folios', { n: report.rows.length }) }}</td>
                <td class="px-2 py-2 text-right">{{ formatMoney(report.opening) }}</td><td class="px-2 py-2 text-right">{{ formatMoney(report.received) }}</td><td class="px-2 py-2 text-right">{{ formatMoney(report.applied) }}</td>
                <td class="px-2 py-2 text-right">{{ formatMoney(report.refunded) }}</td><td class="px-2 py-2 text-right" data-testid="dl-closing">{{ formatMoney(report.closing) }}</td><td />
              </tr>
            </tfoot>
          </table>
        </Card>
        <LedgerChecks :checks="report.checks" :reconciled="report.reconciled" :as-of="formatDate(report.as_of)" />
      </template>
    </template>
  </section>
</template>
