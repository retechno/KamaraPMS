<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { DailyFlash, FlashLine } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { DateInput } from '@/components/ui/date-input'
import { t } from '@/i18n'
import { formatDate, formatDateTime, formatMoney } from '@/utils/format'
import { guardForm } from '@/utils/forms'
import DayReportActions from './DayReportActions.vue'
import FlashRevenueTable from './FlashRevenueTable.vue'
import FlashTable from './FlashTable.vue'
import { useDayReport } from './useDayReport'

/** The daily flash report (docs/architecture/38-reports-night-audit-pack.md 7.1, mockup 63): the day, the month and the year beside last month, last year and the budget. */
const date = ref('')
const { allowed, denied, data, error, busy, run, csv, pdf } = useDayReport<DailyFlash>('daily-flash', () => ({ date: date.value || undefined }))
watch(() => data.value?.business_date, (d) => {
  if (d) date.value = d
})
watch(allowed, (ok) => {
  if (ok) void run()
}, { immediate: true })

const flash = computed(() => data.value)
const hasBudget = computed(() => flash.value?.has_budget === true)
const source = (key: string): FlashLine | undefined => flash.value?.revenue.by_source.find((l) => l.key === key)
const diffOpen = computed(() => !!flash.value?.control && !flash.value.control.balanced)
const warn = (w: { code: string; params?: Record<string, string> }): string => t(`dayReports.flash.warn.${w.code}` as 'dayReports.flash.warn.CONTROL_DIFFERENCE', Object.fromEntries(Object.entries(w.params ?? {}).map(([k, v]) => [k, /^-?\d+(\.\d+)?$/.test(v) ? formatMoney(v) : v])))
const h = 'mb-2 mt-0 text-sm font-semibold'
</script>

<template>
  <section class="flex flex-col gap-4" data-testid="daily-flash">
    <PageHeader :title="t('dayReports.flash.title')" :description="t('dayReports.flash.intro')">
      <template #actions>
        <DayReportActions :ready="!!flash" :busy="busy" @csv="csv(date)" @pdf="pdf" />
      </template>
    </PageHeader>
    <ErrorNotice :error="error" inline data-testid="report-error" />
    <p v-if="denied" class="muted m-0" data-testid="no-access">{{ t('dayReports.denied', { permission: 'report.view' }) }}</p>
    <template v-else>
      <form class="mb-5 flex flex-wrap items-end gap-3 print:hidden" novalidate @submit.prevent="guardForm($event) && run()">
        <FormField :label="t('dayReports.businessDate')"><template #default="{ id }"><DateInput :id="id" v-model="date" name="date" data-testid="flash-date" /></template></FormField>
        <Button type="submit" size="sm" :disabled="busy" data-testid="run">{{ t('dayReports.run') }}</Button>
      </form>

      <template v-if="flash">
        <p v-if="flash.interim" class="m-0 rounded-md border border-border bg-muted px-3 py-2 text-sm text-warning-text" data-testid="flash-interim">{{ t('dayReports.flash.interim', { date: formatDate(flash.business_date) }) }}</p>
        <ul v-if="flash.warnings.length" class="m-0 flex list-none flex-col gap-1 p-0 text-sm text-warning-text" data-testid="flash-warnings">
          <li v-for="w in flash.warnings" :key="w.code" :data-testid="`warn-${w.code}`">{{ warn(w) }}</li>
        </ul>

        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" data-testid="flash-summary">
          <Card class="p-4"><div class="text-xs uppercase text-muted-foreground">{{ t('dayReports.flash.rows.total_net') }}</div><div class="text-xl font-semibold tabular-nums" data-testid="sum-net">{{ formatMoney(flash.revenue.total_net.today) }}</div><div class="text-xs text-muted-foreground">{{ t('dayReports.flash.rows.gtr') }} {{ formatMoney(flash.revenue.gtr.today) }}</div></Card>
          <Card class="p-4"><div class="text-xs uppercase text-muted-foreground">{{ t('dayReports.flash.rows.source_hotel') }}</div><div class="text-xl font-semibold tabular-nums">{{ formatMoney(source('source_hotel')?.today) }}</div></Card>
          <Card class="p-4"><div class="text-xs uppercase text-muted-foreground">{{ t('dayReports.flash.rows.source_pos') }}</div><div class="text-xl font-semibold tabular-nums">{{ formatMoney(source('source_pos')?.today) }}</div></Card>
          <Card class="p-4"><div class="text-xs uppercase text-muted-foreground">{{ t('dayReports.flash.rows.source_banquet') }}</div><div class="text-xl font-semibold tabular-nums">{{ formatMoney(source('source_banquet')?.today) }}</div></Card>
        </div>

        <Card class="overflow-x-auto p-4"><h2 :class="h">{{ t('dayReports.flash.rooms') }}</h2><FlashTable :lines="flash.rooms" :has-budget="hasBudget" testid="flash-rooms" /></Card>
        <Card v-if="flash.sold_by_type.length" class="overflow-x-auto p-4"><h2 :class="h">{{ t('dayReports.flash.soldByType') }}</h2><FlashTable :lines="flash.sold_by_type" :has-budget="hasBudget" testid="flash-sold" /></Card>
        <Card class="overflow-x-auto p-4"><h2 :class="h">{{ t('dayReports.flash.activity') }}</h2><FlashTable :lines="flash.activity" :has-budget="hasBudget" testid="flash-activity" /></Card>
        <Card v-if="flash.covers.length" class="overflow-x-auto p-4"><h2 :class="h">{{ t('dayReports.flash.covers') }}</h2><FlashTable :lines="flash.covers" :has-budget="hasBudget" testid="flash-covers" /></Card>

        <Card class="overflow-x-auto p-4" data-testid="flash-revenue">
          <h2 :class="h">{{ t('dayReports.flash.revenue') }}</h2>
          <FlashRevenueTable :revenue="flash.revenue" :has-budget="hasBudget" />
        </Card>

        <Card class="overflow-x-auto p-4" data-testid="flash-payments">
          <h2 :class="h">{{ t('dayReports.flash.payments') }}</h2>
          <FlashTable :lines="flash.payments.by_cashier" :heading="t('dayReports.flash.byCashier')" :has-budget="false" testid="flash-by-cashier" />
          <FlashTable :lines="flash.payments.by_method" :heading="t('dayReports.flash.byMethod')" :total="flash.payments.gtp" :has-budget="false" testid="flash-by-method" />
          <FlashTable :lines="flash.payments.refunds" :total="flash.payments.refunds_total" :heading="t('dayReports.flash.refunds')" :has-budget="false" testid="flash-refunds" />
          <FlashTable :lines="[flash.payments.to_city_ledger]" :has-budget="false" testid="flash-city-ledger" />
        </Card>

        <Card v-if="flash.control" class="overflow-x-auto p-4" data-testid="flash-control">
          <h2 :class="h">{{ t('dayReports.flash.control') }} <Badge :variant="flash.control.balanced ? 'secondary' : 'destructive'" data-testid="control-badge">{{ flash.control.balanced ? t('dayReports.matches') : t('dayReports.differs') }}</Badge></h2>
          <table class="w-full border-collapse text-sm tabular-nums">
            <thead><tr class="border-b border-border"><th class="px-2 py-1.5 text-left text-[11px] font-medium uppercase text-muted-foreground" /><th class="px-2 py-1.5 text-right text-[11px] font-medium uppercase text-muted-foreground">{{ t('dayReports.flash.today') }}</th><th class="px-2 py-1.5 text-right text-[11px] font-medium uppercase text-muted-foreground">{{ t('dayReports.flash.mtd') }}</th></tr></thead>
            <tbody>
              <tr v-for="k in (['gtr', 'gtp', 'refunds', 'to_city_ledger', 'expected_change', 'actual_change', 'difference'] as const)" :key="k" class="border-b border-border" :data-testid="`control-${k}`">
                <td class="px-2 py-1.5">{{ t(`dayReports.flash.controlRows.${k}` as 'dayReports.flash.controlRows.gtr') }}</td>
                <td :class="['px-2 py-1.5 text-right', k === 'difference' && diffOpen && 'text-destructive']">{{ formatMoney(flash.control.today[k]) }}</td>
                <td :class="['px-2 py-1.5 text-right', k === 'difference' && diffOpen && 'text-destructive']">{{ formatMoney(flash.control.mtd[k]) }}</td>
              </tr>
            </tbody>
          </table>
        </Card>

        <p class="m-0 text-xs text-muted-foreground" data-testid="flash-signatures">
          {{ t('dayReports.flash.preparedBy', { name: flash.signatures.prepared_by || '–', at: flash.signatures.prepared_at ? formatDateTime(flash.signatures.prepared_at) : '–' }) }}
          · {{ t('dayReports.flash.approvedBy') }}
        </p>
      </template>
    </template>
  </section>
</template>
