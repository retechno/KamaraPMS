<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import type { BanquetForecastGroup, BanquetForecastReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { Badge } from '@/components/ui/badge'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatMoney, formatPercent } from '@/utils/format'

/**
 * The forecast of revenue (docs/architecture/36-banquet.md 4.10, mockup 50): the net revenue the sessions of the period are expected to bring, by month, by kind of event and by sales person. The definite
 * events and the tentative ones are on rows of their own and are never added. The toggle adds service and tax.
 */
const property = usePropertyStore()
const withTax = ref(false)
const r = useInventoryReport<BanquetForecastReport>('/api/v1/properties/{propertyId}/banquet/reports/forecast', 'banquet-forecast.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/reports/forecast', { params: { path: { propertyId }, query: { from: p.from, to: p.to, with_tax: withTax.value } } })
  return data ?? null
})

// the forecast looks forward: from the first of the month of the business date for six months
function defaults(): void {
  const bd = property.clock?.business_date ?? new Date().toISOString().slice(0, 10)
  const end = new Date(Date.UTC(Number(bd.slice(0, 4)), Number(bd.slice(5, 7)) + 5, 0))
  r.period.from = `${bd.slice(0, 8)}01`
  r.period.to = end.toISOString().slice(0, 10)
}
watch(r.pid, () => {
  if (!r.period.from) defaults()
  void r.load()
}, { immediate: true })
watch(withTax, () => void r.load())

const money = (v: string): string => (Number(v) === 0 ? '–' : formatMoney(v))
const groups = computed<{ key: string; title: string; rows: BanquetForecastGroup[] }[]>(() => {
  const d = r.data.value
  return d ? [
    { key: 'months', title: t('banquet.reports.forecast.byMonth'), rows: d.months },
    { key: 'types', title: t('banquet.reports.forecast.byType'), rows: d.by_type },
    { key: 'sales', title: t('banquet.reports.forecast.bySales'), rows: d.by_sales },
  ] : []
})
const label = (key: string, g: BanquetForecastGroup): string => (key === 'months' ? g.key : g.name || t('banquet.reports.forecast.noType'))
</script>

<template>
  <PageHeader :title="t('banquet.reports.forecast.title')" :description="t('banquet.reports.forecast.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv({ with_tax: withTax })">
      <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="withTax" type="checkbox" name="with_tax" class="size-4 accent-primary" data-testid="forecast-tax" />{{ t('banquet.reports.forecast.withTax') }}</label>
    </ReportPeriodBar>
    <template v-if="r.data.value">
      <dl class="m-0 mb-4 grid grid-cols-2 gap-3 text-sm md:grid-cols-4" data-testid="forecast-kpi">
        <div class="rounded-md border border-border p-3"><dt class="text-muted-foreground">{{ t('banquet.reports.forecast.conversion') }}</dt><dd class="m-0 text-lg font-semibold" data-testid="kpi-conversion">{{ r.data.value.conversion_pct === null ? '–' : formatPercent(r.data.value.conversion_pct) }}</dd></div>
        <div class="rounded-md border border-border p-3"><dt class="text-muted-foreground">{{ t('banquet.reports.forecast.overdue') }}</dt><dd class="m-0 text-lg font-semibold" data-testid="kpi-overdue">{{ r.data.value.tentative_overdue }}</dd></div>
        <div class="rounded-md border border-border p-3"><dt class="text-muted-foreground">{{ t('banquet.reports.forecast.unposted') }}</dt><dd class="m-0 text-lg font-semibold" data-testid="kpi-unposted"><RouterLink to="/banquet/unposted" class="text-primary hover:underline">{{ r.data.value.unposted }}</RouterLink></dd></div>
        <div class="rounded-md border border-border p-3"><dt class="text-muted-foreground">{{ t('banquet.reports.forecast.oldest') }}</dt><dd class="m-0 text-lg font-semibold">{{ t('banquet.reports.forecast.days', { n: r.data.value.unposted_oldest_days }) }}</dd></div>
      </dl>
      <p v-if="!r.data.value.months.length" class="m-0 text-sm text-muted-foreground" data-testid="forecast-empty">{{ t('banquet.reports.empty') }}</p>
      <section v-for="g in groups" :key="g.key" class="mb-6" :data-testid="`forecast-${g.key}`">
        <h2 class="mb-2 text-base font-semibold">{{ g.title }}</h2>
        <div class="overflow-x-auto">
          <table class="w-full min-w-[32rem] text-sm">
            <thead><tr class="text-left text-xs text-muted-foreground"><th class="py-1 pr-2 font-medium" /><th class="py-1 pr-2 font-medium" /><th class="py-1 pr-2 text-right font-medium">{{ t('banquet.reports.forecast.amount') }}</th><th class="py-1 text-right font-medium">{{ t('banquet.reports.forecast.events') }}</th></tr></thead>
            <tbody>
              <template v-for="x in g.rows" :key="x.key">
                <tr class="border-t border-border" :data-testid="`forecast-${g.key}-${x.key}-definite`">
                  <td class="py-1.5 pr-2 font-medium">{{ label(g.key, x) }}</td><td class="py-1.5 pr-2"><Badge variant="success">{{ t('banquet.reports.forecast.definite') }}</Badge></td>
                  <td class="py-1.5 pr-2 text-right">{{ money(x.definite) }}</td><td class="py-1.5 text-right">{{ x.definite_events }}</td>
                </tr>
                <tr :data-testid="`forecast-${g.key}-${x.key}-tentative`">
                  <td class="py-1.5 pr-2" /><td class="py-1.5 pr-2"><Badge variant="outline">{{ t('banquet.reports.forecast.tentative') }}</Badge></td>
                  <td class="py-1.5 pr-2 text-right text-muted-foreground">{{ money(x.tentative) }}</td><td class="py-1.5 text-right text-muted-foreground">{{ x.tentative_events }}</td>
                </tr>
              </template>
            </tbody>
          </table>
        </div>
      </section>
      <p class="text-xs text-muted-foreground">{{ t('banquet.reports.forecast.note') }}</p>
    </template>
  </template>
</template>
