<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { GuestLedgerReport } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { formatBalance, formatDate, formatDateShort, formatMoney } from '@/utils/format'
import { money } from '@/views/accounting/reportApi'
import DayReportActions from './DayReportActions.vue'
import LedgerChecks from './LedgerChecks.vue'
import { folioType } from './ledgerLabels'
import { useDayReport } from './useDayReport'

/** The guest ledger of a closed business day (docs/architecture/38-reports-night-audit-pack.md 7.3, mockup 65): the balance of every folio, from the ledger, for any past date. */
const asOf = ref('')
const { allowed, denied, data, error, busy, run, csv, pdf } = useDayReport<GuestLedgerReport>('guest-ledger', () => ({ as_of: asOf.value || undefined }))
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
  <section class="flex flex-col gap-4" data-testid="guest-ledger">
    <PageHeader :title="t('dayReports.guestLedger.title')" :description="t('dayReports.guestLedger.intro')">
      <template #actions>
        <DayReportActions :ready="!!report" :busy="busy" @csv="csv(asOf)" @pdf="pdf" />
      </template>
    </PageHeader>
    <ErrorNotice :error="error" inline data-testid="report-error" />
    <p v-if="denied" class="muted m-0" data-testid="no-access">{{ t('dayReports.denied', { permission: 'report.view' }) }}</p>
    <template v-else>
      <form class="flex flex-wrap items-end gap-3 print:hidden" novalidate @submit.prevent="run">
        <FormField :label="t('dayReports.asOf')"><template #default="{ id }"><Input :id="id" v-model="asOf" type="date" name="as_of" data-testid="gl-as-of" /></template></FormField>
        <Button type="submit" size="sm" :disabled="busy" data-testid="run">{{ t('dayReports.run') }}</Button>
        <Button as-child variant="outline" size="sm"><RouterLink to="/reports/deposit-ledger" data-testid="to-deposit-ledger">{{ t('dayReports.depositLedger.title') }}</RouterLink></Button>
      </form>

      <template v-if="report">
        <EmptyState v-if="!report.rows.length" :title="t('dayReports.empty')" :description="t('emptyState.changeSelection')" data-testid="empty" />
        <Card v-else class="overflow-x-auto p-0">
          <table class="w-full border-collapse text-xs tabular-nums" data-testid="gl-table">
            <thead>
              <tr class="border-b border-border">
                <th :class="[th, 'text-left']">{{ t('dayReports.guestLedger.folio') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.guestLedger.owner') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.guestLedger.stay') }}</th>
                <th :class="[th, 'text-right']">{{ t('dayReports.guestLedger.charges') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.guestLedger.settlements') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.guestLedger.deposits') }}</th>
                <th :class="[th, 'text-right']">{{ t('dayReports.guestLedger.balance') }}</th><th :class="[th, 'text-right']">{{ t('dayReports.guestLedger.held') }}</th><th :class="[th, 'text-left']">{{ t('dayReports.guestLedger.age') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in report.rows" :key="r.folio_id" class="border-b border-border align-top" :data-testid="`gl-row-${r.folio_number}`">
                <td class="px-2 py-2">{{ r.folio_number }}<div class="text-muted-foreground">{{ folioType(r.folio_type) }} · {{ r.status === 'OPEN' ? t('dayReports.guestLedger.open') : t('dayReports.guestLedger.closedWithBalance') }}</div></td>
                <td class="px-2 py-2">{{ r.owner }}<div v-if="r.room" class="text-muted-foreground">{{ r.room }}</div><div v-if="r.confirmation_number" class="text-muted-foreground">{{ r.confirmation_number }}</div></td>
                <td class="px-2 py-2">{{ r.arrival ? `${formatDateShort(r.arrival)} – ${formatDate(r.departure)}` : '' }}</td>
                <td class="px-2 py-2 text-right">{{ money(r.charges) }}</td><td class="px-2 py-2 text-right">{{ money(r.settlements) }}</td><td class="px-2 py-2 text-right">{{ money(r.deposits) }}</td>
                <td class="px-2 py-2 text-right font-medium">{{ formatBalance(r.balance) }}</td><td class="px-2 py-2 text-right">{{ money(r.deposit_held) }}</td>
                <td class="px-2 py-2">{{ r.age_bucket ? t(`dayReports.guestLedger.bucket.${r.age_bucket}` as 'dayReports.guestLedger.bucket.0-1') : '' }}</td>
              </tr>
            </tbody>
            <tfoot>
              <tr class="font-semibold" data-testid="gl-total">
                <td class="px-2 py-2" colspan="3">{{ t('dayReports.total') }} · {{ t('dayReports.guestLedger.folios', { n: report.rows.length }) }}</td>
                <td class="px-2 py-2 text-right">{{ formatMoney(report.charges) }}</td><td class="px-2 py-2 text-right">{{ formatMoney(report.settlements) }}</td><td class="px-2 py-2 text-right">{{ formatMoney(report.deposits) }}</td>
                <td class="px-2 py-2 text-right" data-testid="gl-balance">{{ formatMoney(report.balance) }}</td><td class="px-2 py-2 text-right" data-testid="gl-held">{{ formatMoney(report.deposit_held) }}</td><td />
              </tr>
            </tfoot>
          </table>
        </Card>

        <LedgerChecks :checks="report.checks" :reconciled="report.reconciled" :as-of="formatDate(report.as_of)" />

        <Card class="p-4" data-testid="gl-aging">
          <h2 class="mb-2 mt-0 text-sm font-semibold">{{ t('dayReports.guestLedger.aging') }} <Badge v-if="report.release_mode" variant="outline">{{ t(`dayReports.releaseMode.${report.release_mode}` as 'dayReports.releaseMode.EVERY_DAY') }}</Badge></h2>
          <ul class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
            <li v-for="a in report.aging" :key="a.bucket" class="flex justify-between gap-3" :data-testid="`gl-age-${a.bucket}`"><span>{{ t(`dayReports.guestLedger.bucket.${a.bucket}` as 'dayReports.guestLedger.bucket.0-1') }} · {{ t('dayReports.guestLedger.folios', { n: a.folios }) }}</span><span class="tabular-nums">{{ formatMoney(a.balance) }}</span></li>
          </ul>
        </Card>
      </template>
    </template>
  </section>
</template>
