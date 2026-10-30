<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetFiscalReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { t } from '@/i18n'
import { formatDate, formatMoney, formatMonth } from '@/utils/format'
import { downloadCsv, money } from '@/views/accounting/reportApi'
import { fiscalGroupLabel, fiscalGroupName, ratePercent } from './fiscalGroup'

/**
 * Commercial against fiscal depreciation for a tax year, and the correction of the annual return (docs/architecture/37-fixed-assets.md 4.9, mockup 60). It is computed from the rates of the fiscal groups
 * and nothing is posted. Every rate is data that the tax consultant has to confirm, and the page says so.
 */
const { pid, canRead } = useAssetAccess()
const year = ref('')
const report = ref<AssetFiscalReport | null>(null)
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null || !canRead.value) {
    loaded.value = true
    return
  }
  busy.value = true
  try {
    const y = Number(year.value)
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/reports/fiscal', { params: { path: { propertyId }, query: { tax_year: Number.isInteger(y) && y > 0 ? y : undefined } } })
    // the endpoint also answers text/csv, so the generated type of the body is a union with string
    const body = typeof data === 'string' ? null : (data ?? null)
    report.value = body
    if (body) year.value = String(body.tax_year)
  } catch (e) {
    report.value = null
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
    await downloadCsv('/api/v1/properties/{propertyId}/assets/reports/fiscal', { path: { propertyId }, query: { tax_year: Number(year.value) || undefined } }, 'asset-fiscal.csv')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
function print(): void {
  window.print()
}

/** A correction with its sign: positive when the commercial figure is the larger one. */
const signed = (v: string): string => (Number(v) > 0 ? `+${formatMoney(v)}` : Number(v) < 0 ? `−${formatMoney(String(v).replace('-', ''))}` : '0')
const notes = (r: AssetFiscalReport['rows'][number]): string[] => {
  const out: string[] = []
  if (r.partial_year) out.push(t('assets.fiscalReport.note.partial'))
  if (r.fiscal_estimated) out.push(t('assets.fiscalReport.note.estimated'))
  if (r.from_opening) out.push(t('assets.fiscalReport.note.opening'))
  if (r.disposed_in_year) out.push(t('assets.fiscalReport.note.disposed'))
  return out
}
/** The group by its name and the yearly rate of the method of the asset, never by its code. */
const groupText = (r: AssetFiscalReport['rows'][number]): string => fiscalGroupLabel(r.fiscal_group, r.fiscal_rate_percent)
const ym = (date: string): string => date.slice(0, 7)
const prevMonth = (m: string): string => {
  const y = Number(m.slice(0, 4))
  const mo = Number(m.slice(5, 7))
  return mo === 1 ? `${y - 1}-12` : `${y}-${String(mo - 1).padStart(2, '0')}`
}
/** The months of one asset that the commercial and the fiscal figures are both about. */
const rangeText = (r: AssetFiscalReport['rows'][number]): string =>
  r.from_month && r.to_month ? `${t('assets.fiscalReport.range', { from: formatMonth(ym(r.from_month)), to: formatMonth(ym(r.to_month)) })} (${t('assets.fiscalReport.monthsCount', { n: r.months })})` : '–'
/** What the register leaves out of a tax year that starts before it: the months before the register started are in no ledger of it. */
const registerNote = computed(() => {
  const r = report.value
  if (!r || !r.year_starts_before_register || !r.register_from) return ''
  const first = ym(r.register_from)
  const last = ym(r.year_end)
  const before = { from: formatMonth(ym(r.year_start)), to: formatMonth(prevMonth(first)) }
  if (first > last) return t('assets.fiscalReport.registerAfterYear', { register: formatMonth(first), year: r.tax_year })
  return t('assets.fiscalReport.registerBanner', { register: formatMonth(first), year: r.tax_year, from: formatMonth(first), to: formatMonth(last), beforeFrom: before.from, beforeTo: before.to })
})
const openNote = computed(() => (report.value?.year_open && report.value.last_run_month ? t('assets.fiscalReport.yearOpen', { month: formatMonth(ym(report.value.last_run_month)) }) : ''))
const hasNotes = computed(() => (report.value?.rows ?? []).some((r) => notes(r).length))
</script>

<template>
  <section class="flex flex-col gap-4" data-testid="asset-fiscal">
    <PageHeader :title="t('assets.reports.kind.fiscal.name')" :description="t('assets.fiscalReport.intro')">
      <template #actions>
        <Button as-child variant="outline" size="sm"><RouterLink to="/assets/reports">{{ t('assets.reports.back') }}</RouterLink></Button>
        <Button variant="outline" size="sm" data-testid="print" @click="print">{{ t('assets.reports.print') }}</Button>
        <Button variant="outline" size="sm" data-testid="export" :disabled="!report" @click="csv">{{ t('assets.reports.csv') }}</Button>
      </template>
    </PageHeader>
    <ErrorNotice :error="error" />
    <p v-if="loaded && !canRead" class="muted m-0">{{ t('assets.reports.denied') }}</p>
    <template v-else>
      <p class="m-0 rounded-md border border-warning bg-warning/15 p-3 text-sm" role="note" data-testid="fiscal-banner">{{ t('assets.fiscalReport.confirm') }}</p>
      <form class="flex flex-wrap items-end gap-3 print:hidden" @submit.prevent="load">
        <FormField :label="t('assets.fiscalReport.taxYear')"><template #default="{ id }"><Input :id="id" v-model="year" name="tax_year" type="number" min="2000" inputmode="numeric" class="w-28" data-testid="fiscal-year" /></template></FormField>
        <Button type="submit" size="sm" :disabled="busy" data-testid="fiscal-run">{{ t('assets.reports.run') }}</Button>
      </form>
      <template v-if="report">
        <p v-if="registerNote" class="m-0 rounded-md border border-warning bg-warning/15 p-3 text-sm" role="note" data-testid="fiscal-register-banner">{{ registerNote }}</p>
        <p class="m-0 text-sm text-muted-foreground" data-testid="fiscal-period">{{ t('assets.fiscalReport.period', { year: report.tax_year, from: formatDate(report.year_start), to: formatDate(report.year_end) }) }}<template v-if="openNote"> · <span data-testid="fiscal-open">{{ openNote }}</span></template></p>
        <Card class="overflow-x-auto p-0">
          <table class="w-full border-collapse text-sm tabular-nums" data-testid="fiscal-table">
            <thead>
              <tr class="border-b border-border text-xs uppercase text-muted-foreground">
                <th class="p-2 text-left">{{ t('assets.fiscalReport.asset') }}</th><th class="p-2 text-left">{{ t('assets.fiscalReport.group') }}</th><th class="p-2 text-left">{{ t('assets.fiscalReport.months') }}</th>
                <th class="p-2 text-right">{{ t('assets.fiscalReport.commercial') }}</th><th class="p-2 text-right">{{ t('assets.fiscalReport.fiscal') }}</th><th class="p-2 text-right">{{ t('assets.fiscalReport.correction') }}</th>
                <th v-if="hasNotes" class="p-2 text-left">{{ t('assets.fiscalReport.notes') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in report.rows" :key="r.asset_id" class="border-b border-border" :data-testid="`fiscal-row-${r.number}`">
                <td class="p-2"><RouterLink :to="`/assets/${r.asset_id}`">{{ r.number }}</RouterLink> · {{ r.name }}</td>
                <td class="p-2" :data-testid="`fiscal-group-${r.number}`">{{ groupText(r) }} <Badge variant="outline" class="ml-1" data-testid="rate-confirm">{{ t('assets.fiscalReport.rateBadge') }}</Badge></td>
                <td class="p-2 text-xs" :data-testid="`fiscal-range-${r.number}`">{{ rangeText(r) }}<template v-if="r.until_last_run && r.to_month"><br><span class="text-muted-foreground">{{ t('assets.fiscalReport.untilLastRun', { month: formatMonth(ym(r.to_month)) }) }}</span></template></td>
                <td class="p-2 text-right">{{ formatMoney(r.commercial) }}</td><td class="p-2 text-right">{{ formatMoney(r.fiscal) }}</td><td class="p-2 text-right font-medium">{{ signed(r.difference) }}</td>
                <td v-if="hasNotes" class="p-2 text-xs text-muted-foreground">{{ notes(r).join(' · ') }}</td>
              </tr>
              <tr v-if="!report.rows.length"><td :colspan="hasNotes ? 7 : 6" class="p-3 muted">{{ t('assets.fiscalReport.none') }}</td></tr>
            </tbody>
            <tfoot>
              <tr class="font-semibold" data-testid="fiscal-total">
                <td class="p-2" colspan="3">{{ t('assets.fiscalReport.total', { n: report.rows.length }) }}</td>
                <td class="p-2 text-right">{{ formatMoney(report.totals.commercial) }}</td><td class="p-2 text-right">{{ formatMoney(report.totals.fiscal) }}</td><td class="p-2 text-right">{{ signed(report.totals.depreciation_net) }}</td>
                <td v-if="hasNotes" />
              </tr>
            </tfoot>
          </table>
        </Card>
        <Card v-if="report.disposals.length" class="flex flex-col gap-2 p-4" data-testid="fiscal-disposals">
          <h2 class="m-0 text-base font-semibold">{{ t('assets.fiscalReport.disposalsTitle') }}</h2>
          <div class="overflow-x-auto">
            <table class="w-full border-collapse text-sm tabular-nums">
              <thead><tr class="border-b border-border text-xs uppercase text-muted-foreground"><th class="p-2 text-left">{{ t('assets.fiscalReport.asset') }}</th><th class="p-2 text-left">{{ t('assets.fiscalReport.disposal') }}</th><th class="p-2 text-right">{{ t('assets.fiscalReport.proceeds') }}</th><th class="p-2 text-right">{{ t('assets.fiscalReport.commercialGain') }}</th><th class="p-2 text-right">{{ t('assets.fiscalReport.fiscalGain') }}</th><th class="p-2 text-right">{{ t('assets.fiscalReport.correction') }}</th></tr></thead>
              <tbody>
                <tr v-for="d in report.disposals" :key="d.disposal_number" class="border-b border-border" :data-testid="`fiscal-disposal-${d.disposal_number}`">
                  <td class="p-2">{{ d.number }} · {{ d.name }}</td><td class="p-2">{{ d.disposal_number }} · {{ formatDate(d.disposal_date) }}</td>
                  <td class="p-2 text-right">{{ money(d.proceeds) }}</td><td class="p-2 text-right">{{ signed(d.commercial_gain) }}</td><td class="p-2 text-right">{{ signed(d.fiscal_gain) }}</td><td class="p-2 text-right font-medium">{{ signed(d.difference) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p class="m-0 text-xs text-muted-foreground">{{ t('assets.fiscalReport.disposalsHint') }}</p>
        </Card>
        <Card class="flex flex-col gap-1 p-4 text-sm" data-testid="fiscal-summary">
          <h2 class="m-0 text-base font-semibold">{{ t('assets.fiscalReport.summaryTitle') }}</h2>
          <div class="flex justify-between"><span>{{ t('assets.fiscalReport.positive') }}</span><b data-testid="fiscal-positive">{{ formatMoney(report.totals.positive_correction) }}</b></div>
          <div class="flex justify-between"><span>{{ t('assets.fiscalReport.negative') }}</span><b data-testid="fiscal-negative">{{ formatMoney(report.totals.negative_correction) }}</b></div>
          <div class="flex justify-between"><span>{{ t('assets.fiscalReport.disposalCorrection') }}</span><b>{{ signed(report.totals.disposal_correction) }}</b></div>
          <div class="flex justify-between border-t border-border pt-1"><span>{{ t('assets.fiscalReport.net') }}</span><b data-testid="fiscal-net">{{ signed(report.totals.net_correction) }}</b></div>
          <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('assets.fiscalReport.positiveHint') }}</p>
          <p v-if="report.totals.partial_year_assets" class="m-0 text-xs text-muted-foreground" data-testid="fiscal-partial">{{ t('assets.fiscalReport.partialCount', { n: report.totals.partial_year_assets }) }}</p>
          <p v-if="report.totals.estimated_fiscal_assets" class="m-0 text-xs text-muted-foreground" data-testid="fiscal-estimated">{{ t('assets.fiscalReport.estimatedCount', { n: report.totals.estimated_fiscal_assets }) }}</p>
        </Card>
        <Card class="flex flex-col gap-2 p-4" data-testid="fiscal-rates">
          <h2 class="m-0 text-base font-semibold">{{ t('assets.fiscalReport.ratesTitle') }}</h2>
          <ul class="m-0 flex list-none flex-wrap gap-2 p-0 text-sm">
            <li v-for="r in report.rates" :key="r.code"><Badge variant="outline" :data-testid="`fiscal-rate-${r.code}`">{{ fiscalGroupName(r.code) }} · {{ t('assets.fiscalReport.years', { n: r.life_years }) }} · SL {{ ratePercent(r.sl_rate) }}<template v-if="r.db_rate"> · DB {{ ratePercent(r.db_rate) }}</template></Badge></li>
          </ul>
          <p class="m-0 text-xs text-muted-foreground">{{ t('assets.fiscalReport.confirm') }}</p>
        </Card>
      </template>
    </template>
  </section>
</template>
