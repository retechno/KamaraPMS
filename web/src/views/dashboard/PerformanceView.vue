<script setup lang="ts">
import { statusSwatch } from '@/components/app/statusMap'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { ArrowDownRight, ArrowUpRight, BedDouble, DoorClosed, DoorOpen, FileText, Landmark, Lock, Percent, RefreshCw, TrendingUp, Users, Wallet } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Dashboard } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import NightsChart, { type NightBar } from '@/components/app/NightsChart.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
import { labelOf } from '@/i18n/labels'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'
import { formatDate, formatMoney, formatMoneyCompact, formatPercent } from '@/utils/format'
import { zoneTime } from '@/utils/zoneTime'

/**
 * The performance of the property, for the managers (`report.view` at the open property): the figures of today or of the month so far with the change against last month, the
 * occupancy of the last closed days and of the nights ahead, the month against the month before, what was received today, and what the companies and the guests in house owe.
 * It reads the dashboard of the API. A person without the permission is told so, and no request is made.
 */
const auth = useAuthStore()
const property = usePropertyStore()

const data = ref<Dashboard | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const loadedAt = ref<Date | null>(null)
type Period = 'today' | 'month'
const period = ref<Period>('today')

const pid = computed(() => property.currentId)
const allowed = computed(() => pid.value !== null && auth.can('report.view', pid.value))

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !allowed.value) return
  busy.value = true
  error.value = null
  try {
    const res = await api.GET('/api/v1/properties/{propertyId}/dashboard', { params: { path: { propertyId } } })
    data.value = res.data ?? null
    loadedAt.value = new Date()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  data.value = null
  void load()
}, { immediate: true })

const today = computed(() => data.value?.today ?? null)
const currency = computed(() => property.current?.currency_code ?? 'IDR')
const short = (amount: string | undefined): string => formatMoneyCompact(amount ?? '0', currency.value)
const updatedAt = computed(() => (loadedAt.value ? zoneTime(loadedAt.value, property.clock?.timezone) : ''))

// Room charges are posted by night audit, so until it has run nothing is "sold" and ADR, RevPAR and the revenue would read 0 where they are simply unknown.
const awaitingAudit = computed(() => period.value === 'today' && today.value?.rooms.sold === 0)
const afterAudit = (amount: string | undefined): { value: string; hint: string } =>
  awaitingAudit.value ? { value: '–', hint: t('dashboard.manager.availableAfterAudit') } : { value: short(amount), hint: '' }

// A bar is as tall as the occupancy it shows (0 to 100%).
const bar = (percent: string): string => `${Math.min(100, Math.max(0, Number(percent)))}%`

// The change of a figure against the same days of the month before: nothing to compare with when that was zero.
function delta(now: string, before: string): { text: string; up: boolean } | null {
  const a = Number(now)
  const b = Number(before)
  if (!b) return null
  const pct = ((a - b) / b) * 100
  return { text: `${pct >= 0 ? '▲' : '▼'} ${formatPercent(Math.abs(pct).toFixed(1))}`, up: pct >= 0 }
}

const lastMonthKnown = computed(() => (data.value?.previous_month.days ?? 0) > 0)
const monthKnown = computed(() => (data.value?.month_to_date.days ?? 0) > 0)

const comparison = computed(() => {
  const d = data.value
  if (!d) return []
  const rows: { key: string; label: string; now: string; before: string }[] = [
    { key: 'occupancy', label: t('dashboard.manager.occupancyPct'), now: d.month_to_date.occupancy_percent, before: d.previous_month.occupancy_percent },
    { key: 'adr', label: t('dashboard.manager.adr'), now: d.month_to_date.adr, before: d.previous_month.adr },
    { key: 'revpar', label: t('dashboard.manager.revpar'), now: d.month_to_date.revpar, before: d.previous_month.revpar },
    { key: 'revenue', label: t('dashboard.manager.roomRevenue'), now: d.month_to_date.room_revenue, before: d.previous_month.room_revenue },
  ]
  const show = (key: string, v: string): string => (key === 'occupancy' ? formatPercent(v) : formatMoney(v))
  // A month with no closed day has no figures: a dash, not a 0 that looks like a result.
  return rows.map((r) => ({ ...r, delta: lastMonthKnown.value ? delta(r.now, r.before) : null, nowText: show(r.key, r.now), beforeText: lastMonthKnown.value ? show(r.key, r.before) : '–' }))
})

/** The four figures on top: today (live, from the open day) or the month so far, each with its change against last month and the line of the closed days. */
type Metric = 'occupancy' | 'adr' | 'revpar' | 'revenue'
const sparkOf = (metric: Metric): number[] =>
  (data.value?.trend ?? []).map((d) => Number(metric === 'occupancy' ? d.occupancy_percent : metric === 'adr' ? d.adr : metric === 'revpar' ? d.revpar : d.room_revenue))
const monthNow = (metric: Metric): string => {
  const m = data.value?.month_to_date
  return !m ? '0' : metric === 'occupancy' ? m.occupancy_percent : metric === 'adr' ? m.adr : metric === 'revpar' ? m.revpar : m.room_revenue
}
const monthBefore = (metric: Metric): string => {
  const m = data.value?.previous_month
  return !m ? '0' : metric === 'occupancy' ? m.occupancy_percent : metric === 'adr' ? m.adr : metric === 'revpar' ? m.revpar : m.room_revenue
}
const todayNow = (metric: Metric): string => {
  const x = today.value
  return !x ? '0' : metric === 'occupancy' ? x.occupancy_percent : metric === 'adr' ? x.adr : metric === 'revpar' ? x.revpar : x.room_revenue.net
}
const deltaOf = (metric: Metric) => {
  if (!lastMonthKnown.value) return null
  if (period.value === 'month' && !monthKnown.value) return null
  if (period.value === 'today' && awaitingAudit.value && metric !== 'occupancy') return null
  return delta(period.value === 'today' ? todayNow(metric) : monthNow(metric), monthBefore(metric))
}
/** The points of a line for an SVG 100 wide and 24 high; fewer than two days draw nothing. */
function spark(values: number[]): string {
  if (values.length < 2) return ''
  const lo = Math.min(...values)
  const hi = Math.max(...values)
  const span = hi - lo || 1
  return values.map((v, i) => `${((i / (values.length - 1)) * 100).toFixed(1)},${(22 - ((v - lo) / span) * 20).toFixed(1)}`).join(' ')
}

const mainKpis = computed(() => {
  const occupancy = period.value === 'today'
    ? { value: formatPercent(today.value?.occupancy_percent ?? '0.00'), hint: t('dashboard.manager.roomsOf', { occupied: today.value?.rooms.occupied ?? 0, sellable: today.value?.rooms.sellable ?? 0 }) }
    : monthKnown.value ? { value: formatPercent(monthNow('occupancy')), hint: t('performance.monthDays', { n: data.value?.month_to_date.days ?? 0 }) } : { value: '–', hint: t('dashboard.manager.noClosedDay') }
  const money = (metric: Metric): { value: string; hint: string } => {
    if (period.value === 'today') return afterAudit(todayNow(metric))
    return monthKnown.value ? { value: short(monthNow(metric)), hint: t('performance.monthDays', { n: data.value?.month_to_date.days ?? 0 }) } : { value: '–', hint: t('dashboard.manager.noClosedDay') }
  }
  return [
    { id: 'occupancy', label: t('dashboard.manager.occupancyToday'), icon: Percent, ...occupancy },
    { id: 'adr', label: t('dashboard.manager.adr'), icon: TrendingUp, ...money('adr') },
    { id: 'revpar', label: t('dashboard.manager.revpar'), icon: BedDouble, ...money('revpar') },
    { id: 'revenue', label: period.value === 'today' ? t('dashboard.manager.revenueToday') : t('dashboard.manager.roomRevenue'), icon: Wallet, ...money('revenue') },
  ].map((k) => ({ ...k, delta: deltaOf(k.id as Metric), line: spark(sparkOf(k.id as Metric)) }))
})

// The day and month of a date ("9 Okt"): the label under a bar, which has no room for the year.
const shortDate = (iso: string): string => formatDate(iso).replace(/ \d{4}$/, '')

// The trend is always at least a week wide: the days before the first closed day are empty bars, so one closed day is not one bar as wide as the card.
const MIN_TREND_DAYS = 7
const trendDays = computed(() => {
  const days = data.value?.trend ?? []
  const first = days[0]?.business_date
  if (!first || days.length >= MIN_TREND_DAYS) return { empty: [] as string[], days }
  return { empty: Array.from({ length: MIN_TREND_DAYS - days.length }, (_, i) => addDays(first, i - (MIN_TREND_DAYS - days.length))), days }
})
const trendSummary = computed(() => {
  const days = data.value?.trend ?? []
  if (!days.length) return ''
  const best = days.reduce((a, b) => (Number(b.occupancy_percent) > Number(a.occupancy_percent) ? b : a))
  const average = days.reduce((sum, d) => sum + Number(d.occupancy_percent), 0) / days.length
  return t('performance.trendSummary', { average: formatPercent(average.toFixed(2)), best: formatPercent(best.occupancy_percent), day: shortDate(best.business_date) })
})
const forecastBars = computed<NightBar[]>(() =>
  (data.value?.forecast ?? []).map((d) => ({
    key: d.date,
    percent: Number(d.occupancy_percent),
    label: shortDate(d.date),
    short: String(Number(d.date.slice(8))),
    title: `${formatDate(d.date)}: ${d.rooms_booked}/${d.rooms_sellable}`,
  })),
)
const rooms = computed(() => {
  const r = data.value?.rooms
  if (!r) return []
  return [
    { key: 'clean', label: t('dashboard.manager.clean'), value: r.clean, dot: statusSwatch('housekeeping', 'CLEAN').dot },
    { key: 'inspected', label: t('dashboard.manager.inspected'), value: r.inspected, dot: statusSwatch('housekeeping', 'INSPECTED').dot },
    { key: 'cleaning', label: t('dashboard.manager.beingCleaned'), value: r.cleaning, dot: statusSwatch('housekeeping', 'CLEANING').dot },
    { key: 'dirty', label: t('dashboard.manager.dirty'), value: r.dirty, dot: statusSwatch('housekeeping', 'DIRTY').dot, warn: r.dirty > 0 },
    { key: 'ooo', label: t('dashboard.manager.outOfOrder'), value: today.value?.rooms.out_of_order ?? 0, dot: statusSwatch('housekeeping', 'BLOCKED').dot },
  ]
})
</script>

<template>
  <PageHeader :title="t('performance.title')" :description="allowed && property.current ? t('performance.description', { property: property.current.name }) : undefined">
    <template v-if="allowed" #marks>
      <Badge variant="outline" data-testid="managers-only">{{ t('performance.managersOnly') }}</Badge>
    </template>
    <template v-if="allowed" #actions>
      <div class="inline-flex gap-1" role="group" :aria-label="t('performance.period')" data-testid="period">
        <Button size="sm" :variant="period === 'today' ? 'default' : 'outline'" :aria-pressed="period === 'today'" data-testid="period-today" @click="period = 'today'">{{ t('performance.today') }}</Button>
        <Button size="sm" :variant="period === 'month' ? 'default' : 'outline'" :aria-pressed="period === 'month'" data-testid="period-month" @click="period = 'month'">{{ t('performance.month') }}</Button>
      </div>
      <span v-if="loadedAt" class="text-sm text-muted-foreground" data-testid="updated">{{ t('dashboard.manager.updated', { time: updatedAt }) }}</span>
      <Button variant="outline" size="sm" :disabled="busy" data-testid="dash-refresh" @click="load">
        <RefreshCw :class="busy && 'animate-spin'" />
        {{ busy ? t('common.loading') : t('common.refresh') }}
      </Button>
      <Button as-child variant="outline" size="sm"><RouterLink to="/reports" data-testid="full-report"><FileText />{{ t('performance.fullReport') }}</RouterLink></Button>
    </template>
  </PageHeader>

  <Card v-if="!allowed" data-testid="no-access">
    <EmptyState :icon="Lock" class="py-16" :title="t('performance.noAccessTitle')" :description="t('performance.noAccessText')" :action-label="t('notFound.back')" action-to="/" />
  </Card>

  <section v-else class="mb-6" data-testid="manager-dashboard">
    <ErrorNotice v-if="error" :error="error" inline data-testid="dash-error" />

    <template v-if="data">
      <div class="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-4" data-testid="main-kpis">
        <KpiCard v-for="k in mainKpis" :key="k.id" :data-testid="`kpi-${k.id}`" :label="k.label" :value="k.value" :icon="k.icon">
          <span v-if="k.hint" class="block">{{ k.hint }}</span>
          <span class="mt-1 flex items-center justify-between gap-2">
            <span v-if="k.delta" :class="cn('inline-flex items-center gap-0.5 text-xs font-medium', k.delta.up ? 'text-success-text' : 'text-destructive')" :data-testid="`delta-${k.id}`">
              <ArrowUpRight v-if="k.delta.up" class="size-3.5" aria-hidden="true" />
              <ArrowDownRight v-else class="size-3.5" aria-hidden="true" />
              {{ k.delta.text }} <span class="font-normal text-muted-foreground">{{ t('performance.vsLastMonth') }}</span>
            </span>
            <svg v-if="k.line" class="h-6 w-16 shrink-0 text-primary" viewBox="0 0 100 24" preserveAspectRatio="none" aria-hidden="true" :data-testid="`spark-${k.id}`">
              <polyline :points="k.line" fill="none" stroke="currentColor" stroke-width="2" vector-effect="non-scaling-stroke" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </span>
        </KpiCard>
      </div>

      <div class="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-4" data-testid="movement-kpis">
        <KpiCard
          data-testid="kpi-arrivals"
          :label="t('dashboard.manager.arrivalsLeft')"
          :value="data.movements.arrivals_expected"
          :hint="t('dashboard.manager.checkedIn', { n: data.movements.arrivals_checked_in })"
          :icon="DoorOpen"
        />
        <KpiCard
          data-testid="kpi-departures"
          :label="t('dashboard.manager.departuresLeft')"
          :value="data.movements.departures_expected"
          :hint="t('dashboard.manager.checkedOut', { n: data.movements.departures_checked_out })"
          :icon="DoorClosed"
        />
        <KpiCard
          data-testid="kpi-inhouse"
          :label="t('dashboard.manager.inHouse')"
          :value="data.movements.in_house"
          :hint="t('dashboard.manager.owe', { amount: formatMoney(data.movements.in_house_balance) })"
          :icon="Users"
        />
        <KpiCard data-testid="kpi-city-ledger" :label="t('dashboard.manager.owedByCompanies')" :value="short(today?.city_ledger.outstanding)" :icon="Landmark">
          <RouterLink to="/city-ledger">{{ t('dashboard.manager.cityLedger') }}</RouterLink>
        </KpiCard>
      </div>

      <div class="mb-4 grid gap-4 lg:grid-cols-2 [&>*]:min-w-0">
        <Card data-testid="trend">
          <CardHeader>
            <CardTitle>{{ t('dashboard.manager.occupancyTrend', { n: trendDays.empty.length + trendDays.days.length }) }}</CardTitle>
            <p v-if="trendSummary" class="m-0 text-sm text-muted-foreground" data-testid="trend-summary">{{ trendSummary }}</p>
          </CardHeader>
          <CardContent>
            <EmptyState v-if="!data.trend.length" :description="t('emptyState.trend')" :title="t('dashboard.manager.nothingClosed')" data-testid="trend-empty" />
            <div v-else class="flex h-40 items-end gap-1.5">
              <div v-for="e in trendDays.empty" :key="e" class="flex h-full min-w-0 flex-1 flex-col items-center justify-end" data-testid="trend-day-empty" aria-hidden="true">
                <span class="block h-0.5 w-full rounded-t-sm bg-muted-foreground/25" />
                <small class="mt-1 text-[10px] text-muted-foreground">{{ shortDate(e) }}</small>
              </div>
              <div
                v-for="d in trendDays.days"
                :key="d.business_date"
                class="flex h-full min-w-0 flex-1 flex-col items-center justify-end"
                data-testid="trend-day"
                :title="`${formatDate(d.business_date)}: ${formatPercent(d.occupancy_percent)} · ${t('dashboard.manager.adr')} ${formatMoney(d.adr)} · ${t('dashboard.manager.revpar')} ${formatMoney(d.revpar)}`"
              >
                <span class="block min-h-0.5 w-full rounded-t-sm bg-primary" data-testid="trend-bar" :style="{ height: bar(d.occupancy_percent) }" />
                <small class="mt-1 text-[10px] text-muted-foreground">{{ shortDate(d.business_date) }}</small>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card data-testid="forecast">
          <CardHeader>
            <CardTitle>{{ t('dashboard.manager.nextNights', { n: data.forecast.length }) }} <small class="font-normal text-muted-foreground">{{ t('dashboard.manager.nextNightsHint') }}</small></CardTitle>
          </CardHeader>
          <CardContent>
            <NightsChart :bars="forecastBars" test-prefix="forecast" />
            <p class="m-0 mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
              <span class="inline-flex items-center gap-1.5"><span class="size-2.5 rounded-sm bg-primary" aria-hidden="true" />{{ t('performance.strong') }}</span>
              <span class="inline-flex items-center gap-1.5"><span class="size-2.5 rounded-sm bg-primary/50" aria-hidden="true" />{{ t('performance.below') }}</span>
            </p>
          </CardContent>
        </Card>
      </div>

      <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3 [&>*]:min-w-0">
        <Card data-testid="month">
          <CardHeader>
            <CardTitle>{{ t('dashboard.manager.monthSoFar') }} <small class="font-normal text-muted-foreground">{{ t('dashboard.manager.againstLastMonth') }}</small></CardTitle>
          </CardHeader>
          <CardContent>
            <p v-if="!data.month_to_date.days" class="m-0 text-sm text-muted-foreground" data-testid="month-empty">{{ t('dashboard.manager.noClosedDay') }}</p>
            <table v-else class="w-full border-collapse text-sm">
              <thead>
                <tr class="text-xs uppercase tracking-wide text-muted-foreground">
                  <th class="py-1.5 text-left font-semibold" />
                  <th class="py-1.5 text-right font-semibold">{{ t('dashboard.manager.thisMonth') }}</th>
                  <th class="py-1.5 text-right font-semibold">{{ t('dashboard.manager.lastMonth') }}</th>
                  <th class="py-1.5 text-right font-semibold">{{ t('dashboard.manager.change') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="r in comparison" :key="r.key" :data-testid="`month-${r.key}`" class="border-t border-border">
                  <td class="py-1.5">{{ r.label }}</td>
                  <td class="py-1.5 text-right font-medium">{{ r.nowText }}</td>
                  <td class="py-1.5 text-right text-muted-foreground">{{ r.beforeText }}</td>
                  <td class="py-1.5 text-right">
                    <span v-if="r.delta" :class="cn('inline-flex items-center gap-0.5', r.delta.up ? 'text-success-text' : 'text-destructive')">
                      <ArrowUpRight v-if="r.delta.up" class="size-3.5" aria-hidden="true" />
                      <ArrowDownRight v-else class="size-3.5" aria-hidden="true" />
                      {{ r.delta.text }}
                    </span>
                    <span v-else class="text-muted-foreground">–</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </CardContent>
        </Card>

        <Card v-if="today?.payments_by_method.length" data-testid="payments">
          <CardHeader><CardTitle>{{ t('dashboard.manager.receivedToday') }}</CardTitle></CardHeader>
          <CardContent>
            <dl class="m-0 divide-y divide-border text-sm">
              <div v-for="m in today.payments_by_method" :key="m.method" class="flex justify-between py-1.5">
                <dt>{{ labelOf('payMethod', m.method) }}</dt>
                <dd class="m-0 font-semibold">{{ $money(m.net) }}</dd>
              </div>
            </dl>
          </CardContent>
        </Card>

        <Card data-testid="housekeeping">
          <CardHeader><CardTitle>{{ t('dashboard.manager.rooms') }}</CardTitle></CardHeader>
          <CardContent>
            <dl class="m-0 divide-y divide-border text-sm">
              <div v-for="r in rooms" :key="r.key" class="flex items-center justify-between py-1.5">
                <dt class="flex items-center gap-2"><span :class="cn('size-2.5 rounded-full', r.dot)" aria-hidden="true" />{{ r.label }}</dt>
                <dd :class="cn('m-0 font-semibold', r.warn && 'text-warning-text')">{{ r.value }}</dd>
              </div>
            </dl>
          </CardContent>
        </Card>
      </div>
    </template>
  </section>
</template>
