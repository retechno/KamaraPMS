<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryDailyCost, InventoryDailyCostDay, InventoryFbPart } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import CostDayPanel from '@/components/inventory/CostDayPanel.vue'
import CostLineChart, { type CostPoint } from '@/components/inventory/CostLineChart.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { Badge } from '@/components/ui/badge'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { formatDate, formatMoney, formatPercent } from '@/utils/format'

/**
 * The daily cost of all costs (docs/architecture/35-inventory.md 12.2, D31, mockup 31): per business day and month to date, in three views over the same rows. The days the close has stored are final; the open day
 * is computed from the ledger and marked "running". A day above its target is marked with a word and a symbol, not only a colour.
 */
type View = 'ALL' | 'FB' | 'DEPT'
const view = ref<View>('ALL')
const chartClass = ref<'FOOD' | 'BEVERAGE'>('FOOD')

const r = useInventoryReport<InventoryDailyCost>('/api/v1/properties/{propertyId}/inventory/reports/daily-cost', 'daily-cost.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/daily-cost', { params: { path: { propertyId }, query: { from: p.from, to: p.to } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })

const money = (v: string | undefined): string => (v === undefined || Number(v) === 0 ? '–' : formatMoney(v))
const kind = (d: InventoryDailyCostDay, k: string): string => d.kinds[k] ?? '0'
const sum = (d: InventoryDailyCostDay, ...ks: string[]): string => String(ks.reduce((n, k) => n + Number(kind(d, k)), 0))
const fbCost = (d: InventoryDailyCostDay): string => sum(d, 'SALE_COST_FOOD', 'SALE_COST_BEVERAGE')
const pct = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : formatPercent(v))
const part = (d: InventoryDailyCostDay, c: 'food' | 'beverage'): InventoryFbPart => d[c]
/** The day whose documents are open under the table (a click on its date in the table of all costs). */
const openDay = ref<string | null>(null)
const dayLabel = (d: InventoryDailyCostDay): string => `${formatDate(d.date)}${d.live ? ` · ${t('inventory.reports.running')}` : ''}`

const rows = computed<InventoryDailyCostDay[]>(() => r.data.value?.days ?? [])
const tabs: { id: View; label: string }[] = [
  { id: 'ALL', label: 'inventory.reports.daily.all' },
  { id: 'FB', label: 'inventory.reports.daily.fb' },
  { id: 'DEPT', label: 'inventory.reports.daily.dept' },
]

const mtd = computed(() => r.data.value?.mtd ?? null)
const targetTone = (p: InventoryFbPart | undefined): 'default' | 'success' | 'danger' => (!p?.pct || !p.target_pct ? 'default' : Number(p.pct) > Number(p.target_pct) ? 'danger' : 'success')
const note = (p: InventoryFbPart | undefined): string => (p && p.no_recipe_days > 0 ? t('inventory.reports.noRecipeDays', { n: p.no_recipe_days }) : '')
const gap = (p: InventoryFbPart | undefined): string => {
  let text: string
  if (p?.no_recipe_data) text = t('inventory.reports.noRecipe')
  else if (!p?.pct || !p.target_pct) text = t('inventory.reports.noTarget')
  else {
    const diff = Number(p.pct) - Number(p.target_pct)
    text = t(diff > 0 ? 'inventory.reports.pointsAbove' : 'inventory.reports.pointsBelow', { target: formatPercent(p.target_pct), points: Math.abs(diff).toFixed(1) })
  }
  return [text, note(p)].filter(Boolean).join(' · ')
}

const points = computed<CostPoint[]>(() => rows.value.map((d) => {
  const p = chartClass.value === 'FOOD' ? d.food : d.beverage
  return {
    key: d.date, label: String(Number(d.date.slice(8))), value: p.pct === null ? null : Number(p.pct), above: p.above_target, noData: p.no_recipe_data,
    title: `${formatDate(d.date)}: ${pct(p.pct)}${p.no_recipe_data ? ` (${t('inventory.reports.noRecipe')})` : ''}${p.target_pct ? ` · ${t('inventory.reports.target')} ${formatPercent(p.target_pct)}` : ''}${p.above_target ? ` · ${t('inventory.reports.aboveTarget')}` : ''}`,
  }
}))
const chartTarget = computed(() => {
  const target = rows.value.map((d) => (chartClass.value === 'FOOD' ? d.food : d.beverage).target_pct).find((x) => x !== null)
  return target ? Number(target) : null
})
const aboveCount = computed(() => rows.value.filter((d) => d.food.above_target || d.beverage.above_target).length)
const departments = computed(() => [{ id: 0, name: t('inventory.reports.noDepartment') }, ...(r.data.value?.departments ?? [])])
const departmentAmount = (d: InventoryDailyCostDay, id: number): string => d.departments[String(id)] ?? '0'
const summaryRows = computed(() => (r.data.value ? [{ id: 'period', label: t('inventory.reports.daily.period'), d: r.data.value.period }, { id: 'mtd', label: t('inventory.reports.daily.mtd'), d: r.data.value.mtd }] : []))
</script>

<template>
  <PageHeader :title="t('inventory.reports.daily.title')" :description="t('inventory.reports.daily.intro')" />
  <ReportPrintHeader :from="r.period.from" :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv()" />

    <CostDayPanel v-if="openDay" :date="openDay" @close="openDay = null" />

    <div v-if="mtd" class="mb-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4" data-testid="daily-kpi">
      <KpiCard :label="t('inventory.reports.daily.kpiFb')" :value="pct(mtd.fb.pct)" :hint="[t('inventory.reports.daily.kpiMtd', { from: formatDate(r.data.value?.mtd_from ?? '') }), note(mtd.fb)].filter(Boolean).join(' · ')" data-testid="kpi-fb" />
      <KpiCard :label="t('inventory.reports.daily.kpiFood')" :value="pct(mtd.food.pct)" :tone="targetTone(mtd.food)" :hint="gap(mtd.food)" data-testid="kpi-food" />
      <KpiCard :label="t('inventory.reports.daily.kpiBeverage')" :value="pct(mtd.beverage.pct)" :tone="targetTone(mtd.beverage)" :hint="gap(mtd.beverage)" data-testid="kpi-beverage" />
      <KpiCard :label="t('inventory.reports.daily.kpiTotal')" :value="formatMoney(mtd.total)" :hint="t('inventory.reports.daily.kpiCostRoom', { amount: mtd.amenity_cost_per_room ? formatMoney(mtd.amenity_cost_per_room) : '–' })" data-testid="kpi-total" />
    </div>

    <div role="tablist" class="mb-3 flex flex-wrap gap-1.5" :aria-label="t('inventory.reports.daily.views')">
      <button v-for="tab in tabs" :key="tab.id" type="button" role="tab" :aria-selected="view === tab.id" class="rounded-md border border-border px-3 py-1.5 text-sm" :class="view === tab.id ? 'bg-primary text-primary-foreground' : 'bg-card'" :data-testid="`tab-${tab.id}`" @click="view = tab.id">{{ t(tab.label) }}</button>
    </div>

    <p v-if="r.loaded.value && rows.length === 0" class="m-0 text-sm text-muted-foreground" data-testid="daily-empty">{{ t('inventory.reports.empty') }}</p>

    <div v-else-if="view === 'ALL'" class="overflow-x-auto" data-testid="daily-all">
      <table class="w-full min-w-[64rem] text-sm">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.date') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.fbCost') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.fbPct') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.waste') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.staffEnt') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.amenities') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.rooms') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.perRoom') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.engineering') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.other') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.countVar') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.priceVar') }}</th>
            <th class="py-1 text-right font-medium">{{ t('inventory.reports.daily.total') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in rows" :key="d.date" class="border-t border-border" :data-testid="`daily-${d.date}`">
            <td class="py-2 pr-2"><button type="button" class="underline print:no-underline" :aria-expanded="openDay === d.date" :data-testid="`daily-open-${d.date}`" @click="openDay = openDay === d.date ? null : d.date">{{ dayLabel(d) }}</button></td>
            <td class="py-2 pr-2 text-right">{{ money(fbCost(d)) }}</td>
            <td class="py-2 pr-2 text-right">{{ pct(d.fb.pct) }}<span v-if="d.fb.no_recipe_days > 0" class="block text-xs font-normal text-muted-foreground" :data-testid="`norecipe-${d.date}`">{{ t('inventory.reports.noRecipe') }}</span></td>
            <td class="py-2 pr-2 text-right">{{ money(kind(d, 'WASTE')) }}</td>
            <td class="py-2 pr-2 text-right">{{ money(sum(d, 'STAFF_MEAL', 'ENTERTAINMENT')) }}</td>
            <td class="py-2 pr-2 text-right">{{ money(kind(d, 'AMENITY')) }}</td>
            <td class="py-2 pr-2 text-right">{{ d.occupied_rooms || '–' }}</td>
            <td class="py-2 pr-2 text-right">{{ d.amenity_cost_per_room ? formatMoney(d.amenity_cost_per_room) : '–' }}</td>
            <td class="py-2 pr-2 text-right">{{ money(kind(d, 'ENGINEERING')) }}</td>
            <td class="py-2 pr-2 text-right">{{ money(kind(d, 'OTHER_USE')) }}</td>
            <td class="py-2 pr-2 text-right">{{ money(kind(d, 'COUNT_VARIANCE')) }}</td>
            <td class="py-2 pr-2 text-right">{{ money(kind(d, 'PRICE_VARIANCE')) }}</td>
            <td class="py-2 text-right font-semibold">{{ money(d.total) }}</td>
          </tr>
          <tr v-for="s in summaryRows" :key="s.id" class="border-t-2 border-border bg-muted/40 font-semibold" :data-testid="`daily-${s.id}`">
            <td class="py-2 pr-2">{{ s.label }}</td>
            <td class="py-2 pr-2 text-right">{{ money(fbCost(s.d)) }}</td><td class="py-2 pr-2 text-right">{{ pct(s.d.fb.pct) }}<span v-if="s.d.fb.no_recipe_days > 0" class="block text-xs font-normal text-muted-foreground" :data-testid="`norecipe-${s.id}`">{{ note(s.d.fb) }}</span></td>
            <td class="py-2 pr-2 text-right">{{ money(kind(s.d, 'WASTE')) }}</td><td class="py-2 pr-2 text-right">{{ money(sum(s.d, 'STAFF_MEAL', 'ENTERTAINMENT')) }}</td>
            <td class="py-2 pr-2 text-right">{{ money(kind(s.d, 'AMENITY')) }}</td><td class="py-2 pr-2 text-right">{{ s.d.occupied_rooms || '–' }}</td><td class="py-2 pr-2 text-right">{{ s.d.amenity_cost_per_room ? formatMoney(s.d.amenity_cost_per_room) : '–' }}</td>
            <td class="py-2 pr-2 text-right">{{ money(kind(s.d, 'ENGINEERING')) }}</td><td class="py-2 pr-2 text-right">{{ money(kind(s.d, 'OTHER_USE')) }}</td>
            <td class="py-2 pr-2 text-right">{{ money(kind(s.d, 'COUNT_VARIANCE')) }}</td><td class="py-2 pr-2 text-right">{{ money(kind(s.d, 'PRICE_VARIANCE')) }}</td>
            <td class="py-2 text-right">{{ money(s.d.total) }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-else-if="view === 'FB'" data-testid="daily-fb">
      <div class="mb-3 flex flex-wrap items-center gap-3">
        <div role="group" :aria-label="t('inventory.reports.daily.chartClass')" class="flex gap-1.5">
          <button v-for="c in (['FOOD', 'BEVERAGE'] as const)" :key="c" type="button" :aria-pressed="chartClass === c" class="rounded-md border border-border px-3 py-1 text-sm" :class="chartClass === c ? 'bg-primary text-primary-foreground' : 'bg-card'" :data-testid="`chart-${c}`" @click="chartClass = c">{{ t(`inventory.reports.class.${c}`) }}</button>
        </div>
        <Badge v-if="aboveCount" variant="warning" data-testid="above-count">▲ {{ t('inventory.reports.daily.daysAbove', { n: aboveCount }) }}</Badge>
      </div>
      <CostLineChart :points="points" :target="chartTarget" :series-label="t('inventory.reports.daily.chartSeries', { class: t(`inventory.reports.class.${chartClass}`) })" test-prefix="cost" class="mb-4 max-w-3xl" />
      <div class="overflow-x-auto">
        <table class="w-full min-w-[56rem] text-sm">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.date') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.foodRevenue') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.foodCost') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.foodPct') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.target') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.bevRevenue') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.bevCost') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.daily.bevPct') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.target') }}</th>
              <th class="py-1 text-right font-medium">{{ t('inventory.reports.daily.mtdPct') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="d in rows" :key="d.date" class="border-t border-border" :data-testid="`fb-${d.date}`">
              <td class="py-2 pr-2">{{ dayLabel(d) }}</td>
              <template v-for="c in (['food', 'beverage'] as const)" :key="c">
                <td class="py-2 pr-2 text-right">{{ money(part(d, c).revenue) }}</td>
                <td class="py-2 pr-2 text-right">{{ money(part(d, c).cost) }}</td>
                <td class="py-2 pr-2 text-right" :class="part(d, c).above_target ? 'font-semibold text-destructive' : ''">
                  <span v-if="part(d, c).above_target" :title="t('inventory.reports.aboveTarget')" :data-testid="`above-${c}-${d.date}`">▲ </span>{{ pct(part(d, c).pct) }}<span v-if="part(d, c).no_recipe_data" class="block text-xs font-normal text-muted-foreground" :data-testid="`norecipe-${c}-${d.date}`">{{ t('inventory.reports.noRecipe') }}</span>
                </td>
                <td class="py-2 pr-2 text-right text-muted-foreground">{{ pct(part(d, c).target_pct) }}</td>
              </template>
              <td class="py-2 text-right">{{ pct(d.fb.mtd_pct) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div v-else class="overflow-x-auto" data-testid="daily-dept">
      <table class="w-full min-w-[40rem] text-sm">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.date') }}</th>
            <th v-for="dep in departments" :key="dep.id" class="py-1 pr-2 text-right font-medium">{{ dep.name }}</th>
            <th class="py-1 text-right font-medium">{{ t('inventory.reports.daily.total') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in rows" :key="d.date" class="border-t border-border" :data-testid="`dept-${d.date}`">
            <td class="py-2 pr-2">{{ dayLabel(d) }}</td>
            <td v-for="dep in departments" :key="dep.id" class="py-2 pr-2 text-right">{{ money(departmentAmount(d, dep.id)) }}</td>
            <td class="py-2 text-right font-semibold">{{ money(d.total) }}</td>
          </tr>
          <tr v-if="r.data.value" class="border-t-2 border-border bg-muted/40 font-semibold">
            <td class="py-2 pr-2">{{ t('inventory.reports.daily.period') }}</td>
            <td v-for="dep in departments" :key="dep.id" class="py-2 pr-2 text-right">{{ money(departmentAmount(r.data.value.period, dep.id)) }}</td>
            <td class="py-2 text-right">{{ money(r.data.value.period.total) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-if="rows.length" class="mt-3 text-xs text-muted-foreground">{{ t('inventory.reports.daily.note') }}</p>
  </template>
</template>
