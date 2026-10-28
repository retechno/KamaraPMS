<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetDifferenceCause, AssetRecapReport, AssetRecapRow } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { t } from '@/i18n'
import { formatDate, formatMoney } from '@/utils/format'
import { downloadCsv, money } from '@/views/accounting/reportApi'

/** The recap by category (docs/architecture/37-fixed-assets.md 5.3, mockup 59): the register beside the general ledger, and what explains a difference. Nothing is blocked by it. */
const { pid, canRead } = useAssetAccess()
const from = ref('')
const to = ref('')
const report = ref<AssetRecapReport | null>(null)
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
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/reports/recap', {
      params: { path: { propertyId }, query: { date_from: from.value || undefined, date_to: to.value || undefined } },
    })
    // the endpoint also answers text/csv, so the generated type of the body is a union with string
    const body = typeof data === 'string' ? null : (data ?? null)
    report.value = body
    if (body) {
      from.value = body.date_from
      to.value = body.date_to
    }
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
    await downloadCsv('/api/v1/properties/{propertyId}/assets/reports/recap', { path: { propertyId }, query: { date_from: from.value || undefined, date_to: to.value || undefined } }, 'asset-recap.csv')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

const isZero = (v: string | undefined): boolean => v === undefined || v === '' || Number(v) === 0
/** The rows whose ledger and register differ, with the parts of the difference. */
interface Difference { row: AssetRecapRow; what: 'cost' | 'accum'; amount: string; causes: AssetDifferenceCause[] }
const differences = computed<Difference[]>(() => {
  const out: Difference[] = []
  for (const row of report.value?.rows ?? []) {
    if (!isZero(row.cost_difference)) out.push({ row, what: 'cost', amount: row.cost_difference, causes: row.cost_causes ?? [] })
    if (!isZero(row.accum_difference)) out.push({ row, what: 'accum', amount: row.accum_difference, causes: row.accum_causes ?? [] })
  }
  return out
})
const accum1590 = computed(() => !isZero(report.value?.account_1590))
function print(): void {
  window.print()
}
const th = 'p-2 text-right text-xs font-medium uppercase text-muted-foreground'
</script>

<template>
  <section class="flex flex-col gap-4" data-testid="asset-recap">
    <PageHeader :title="t('assets.reports.kind.recap.name')" :description="t('assets.reports.recap.intro')">
      <div class="flex flex-wrap items-center gap-2 print:hidden">
        <RouterLink to="/assets/reports" class="text-sm">{{ t('assets.reports.back') }}</RouterLink>
        <Button variant="outline" size="sm" data-testid="print" @click="print">{{ t('assets.reports.print') }}</Button>
        <Button variant="outline" size="sm" data-testid="export" :disabled="!report" @click="csv">{{ t('assets.reports.csv') }}</Button>
      </div>
    </PageHeader>
    <ErrorNotice :error="error" />
    <p v-if="loaded && !canRead" class="muted m-0">{{ t('assets.reports.denied') }}</p>
    <template v-else>
      <form class="flex flex-wrap items-end gap-3 print:hidden" @submit.prevent="load">
        <FormField :label="t('assets.reports.from')"><template #default="{ id }"><Input :id="id" v-model="from" type="date" name="date_from" data-testid="recap-from" /></template></FormField>
        <FormField :label="t('assets.reports.to')"><template #default="{ id }"><Input :id="id" v-model="to" type="date" name="date_to" data-testid="recap-to" /></template></FormField>
        <Button type="submit" size="sm" :disabled="busy" data-testid="recap-run">{{ t('assets.reports.run') }}</Button>
      </form>
      <p v-if="report" class="m-0 text-sm text-muted-foreground" data-testid="recap-period">{{ t('assets.reports.recap.period', { from: formatDate(report.date_from), to: formatDate(report.date_to) }) }}</p>
      <Card v-if="report" class="overflow-x-auto p-0">
        <table class="w-full border-collapse text-sm tabular-nums" data-testid="recap-table">
          <thead>
            <tr class="border-b border-border">
              <th rowspan="2" class="p-2 text-left text-xs font-medium uppercase text-muted-foreground">{{ t('assets.reports.recap.category') }}</th>
              <th colspan="5" class="p-2 text-center text-xs font-medium uppercase text-muted-foreground">{{ t('assets.reports.recap.cost') }}</th>
              <th colspan="6" class="border-l border-border p-2 text-center text-xs font-medium uppercase text-muted-foreground">{{ t('assets.reports.recap.accum') }}</th>
              <th rowspan="2" :class="th">{{ t('assets.reports.recap.carrying') }}</th>
              <th colspan="2" class="border-l border-border p-2 text-center text-xs font-medium uppercase text-muted-foreground">{{ t('assets.reports.recap.ledger') }}</th>
            </tr>
            <tr class="border-b border-border">
              <th :class="th">{{ t('assets.reports.recap.opening') }}</th><th :class="th">{{ t('assets.reports.recap.additions') }}</th><th :class="th">{{ t('assets.reports.recap.reclass') }}</th><th :class="th">{{ t('assets.reports.recap.disposals') }}</th><th :class="th">{{ t('assets.reports.recap.closing') }}</th>
              <th :class="th" class="border-l border-border">{{ t('assets.reports.recap.opening') }}</th><th :class="th">{{ t('assets.reports.recap.additions') }}</th><th :class="th">{{ t('assets.reports.recap.depreciation') }}</th><th :class="th">{{ t('assets.reports.recap.reclass') }}</th><th :class="th">{{ t('assets.reports.recap.disposals') }}</th><th :class="th">{{ t('assets.reports.recap.closing') }}</th>
              <th :class="th" class="border-l border-border">{{ t('assets.reports.recap.ledgerCost') }}</th><th :class="th">{{ t('assets.reports.recap.difference') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in report.rows" :key="r.category_id" class="border-b border-border" :data-testid="`recap-row-${r.category_code}`">
              <td class="p-2">{{ r.category_name }} <span class="text-xs text-muted-foreground">· {{ r.cost_account_code }}{{ r.accumulated_account_code ? `/${r.accumulated_account_code}` : '' }}</span></td>
              <td class="p-2 text-right">{{ money(r.cost_opening) }}</td><td class="p-2 text-right">{{ money(r.cost_additions) }}</td><td class="p-2 text-right">{{ money(r.cost_reclass) }}</td><td class="p-2 text-right">{{ money(r.cost_disposals) }}</td><td class="p-2 text-right font-medium">{{ money(r.cost_closing) }}</td>
              <td class="border-l border-border p-2 text-right">{{ money(r.accum_opening) }}</td><td class="p-2 text-right">{{ money(r.accum_additions) }}</td><td class="p-2 text-right">{{ money(r.accum_depreciation) }}</td><td class="p-2 text-right">{{ money(r.accum_reclass) }}</td><td class="p-2 text-right">{{ money(r.accum_disposals) }}</td><td class="p-2 text-right font-medium">{{ money(r.accum_closing) }}</td>
              <td class="p-2 text-right font-medium">{{ money(r.carrying_closing) }}</td>
              <td class="border-l border-border p-2 text-right">{{ money(r.ledger_cost_closing) }}</td>
              <td class="p-2 text-right" :class="isZero(r.cost_difference) ? '' : 'font-semibold text-warning-foreground'" :data-testid="`recap-diff-${r.category_code}`">{{ isZero(r.cost_difference) ? '0' : formatMoney(r.cost_difference) }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr class="font-semibold" data-testid="recap-total">
              <td class="p-2">{{ t('assets.reports.recap.total') }}</td>
              <td class="p-2 text-right">{{ money(report.total.cost_opening) }}</td><td class="p-2 text-right">{{ money(report.total.cost_additions) }}</td><td class="p-2 text-right">{{ money(report.total.cost_reclass) }}</td><td class="p-2 text-right">{{ money(report.total.cost_disposals) }}</td><td class="p-2 text-right">{{ money(report.total.cost_closing) }}</td>
              <td class="border-l border-border p-2 text-right">{{ money(report.total.accum_opening) }}</td><td class="p-2 text-right">{{ money(report.total.accum_additions) }}</td><td class="p-2 text-right">{{ money(report.total.accum_depreciation) }}</td><td class="p-2 text-right">{{ money(report.total.accum_reclass) }}</td><td class="p-2 text-right">{{ money(report.total.accum_disposals) }}</td><td class="p-2 text-right">{{ money(report.total.accum_closing) }}</td>
              <td class="p-2 text-right">{{ money(report.total.carrying_closing) }}</td>
              <td class="border-l border-border p-2 text-right">{{ money(report.total.ledger_cost_closing) }}</td><td class="p-2 text-right">{{ isZero(report.total.cost_difference) ? '0' : formatMoney(report.total.cost_difference) }}</td>
            </tr>
          </tfoot>
        </table>
      </Card>
      <Card v-if="report" class="flex flex-col gap-2 p-4" data-testid="recap-causes">
        <h2 class="m-0 text-base font-semibold">{{ t('assets.reports.recap.causesTitle') }}</h2>
        <p v-if="!differences.length" class="muted m-0" data-testid="recap-nodiff">{{ t('assets.reports.recap.noDifference') }}</p>
        <ul v-else class="m-0 flex list-none flex-col gap-2 p-0 text-sm">
          <li v-for="d in differences" :key="`${d.row.category_id}-${d.what}`" :data-testid="`recap-cause-${d.row.category_code}-${d.what}`">
            <b>{{ d.row.category_name }}</b> · {{ t(`assets.reports.recap.what.${d.what}`) }}: <span class="font-semibold">{{ formatMoney(d.amount) }}</span>
            <ul class="m-0 mt-1 flex list-none flex-wrap gap-2 p-0">
              <li v-for="c in d.causes" :key="c.code"><Badge :variant="c.code === 'OTHER' ? 'warning' : 'outline'" :data-testid="`cause-${c.code}`">{{ t(`assets.reports.cause.${c.code}`) }} {{ formatMoney(c.amount) }}<template v-if="c.lines"> · {{ t('assets.reports.cause.lines', { n: c.lines }) }}</template></Badge></li>
            </ul>
          </li>
        </ul>
        <p v-if="accum1590" class="m-0 text-xs text-muted-foreground" data-testid="recap-1590">{{ t('assets.reports.recap.note1590', { amount: formatMoney(report.account_1590) }) }}</p>
        <p class="m-0 text-xs text-muted-foreground">{{ t('assets.reports.recap.footnote') }}</p>
      </Card>
    </template>
  </section>
</template>
