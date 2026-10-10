<script setup lang="ts">
import { statusSwatch } from '@/components/app/statusMap'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { ArrowDownRight, ArrowUpRight, BedDouble, DoorClosed, DoorOpen, Landmark, Percent, RefreshCw, TrendingUp, Users, Wallet } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Dashboard } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
import { labelOf } from '@/i18n/labels'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'
import { formatDate, formatDateTime, formatMoney, formatMoneyCompact, formatPercent } from '@/utils/format'

const auth = useAuthStore()
const property = usePropertyStore()

const data = ref<Dashboard | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const loadedAt = ref<Date | null>(null)

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

// Room charges are posted by night audit, so until it has run nothing is "sold" and ADR, RevPAR and the revenue would read 0 where they are simply unknown.
const awaitingAudit = computed(() => today.value?.rooms.sold === 0)
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
  const lastMonthKnown = d.previous_month.days > 0
  return rows.map((r) => ({ ...r, delta: lastMonthKnown ? delta(r.now, r.before) : null, nowText: show(r.key, r.now), beforeText: lastMonthKnown ? show(r.key, r.before) : '–' }))
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
  <section v-if="allowed" class="mb-6" data-testid="manager-dashboard">
    <div class="mb-3 flex flex-wrap items-center gap-3">
      <h2 class="m-0 flex-1 text-lg font-semibold">{{ t('dashboard.manager.title') }}</h2>
      <span v-if="loadedAt" class="text-sm text-muted-foreground">{{ t('dashboard.manager.updated', { time: formatDateTime(loadedAt.toISOString()) }) }}</span>
      <Button variant="outline" size="sm" :disabled="busy" data-testid="dash-refresh" @click="load">
        <RefreshCw :class="busy && 'animate-spin'" />
        {{ busy ? t('common.loading') : t('common.refresh') }}
      </Button>
    </div>
    <ErrorNotice v-if="error" :error="error" inline data-testid="dash-error" />

    <template v-if="data">
      <div class="mb-4 grid grid-cols-[repeat(auto-fill,minmax(10.5rem,1fr))] gap-3">
        <KpiCard
          data-testid="kpi-occupancy"
          :label="t('dashboard.manager.occupancyToday')"
          :value="formatPercent(today?.occupancy_percent ?? '0.00')"
          :hint="t('dashboard.manager.roomsOf', { occupied: today?.rooms.occupied ?? 0, sellable: today?.rooms.sellable ?? 0 })"
          :icon="Percent"
        />
        <KpiCard data-testid="kpi-adr" :label="t('dashboard.manager.adr')" v-bind="afterAudit(today?.adr)" :icon="TrendingUp" />
        <KpiCard data-testid="kpi-revpar" :label="t('dashboard.manager.revpar')" v-bind="afterAudit(today?.revpar)" :icon="BedDouble" />
        <KpiCard data-testid="kpi-revenue" :label="t('dashboard.manager.revenueToday')" v-bind="afterAudit(today?.room_revenue.net)" :icon="Wallet" />
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

      <div class="mb-4 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
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
      </div>

      <div class="grid gap-4 lg:grid-cols-2">
        <Card data-testid="trend">
          <CardHeader><CardTitle>{{ t('dashboard.manager.occupancyTrend', { n: trendDays.empty.length + trendDays.days.length }) }}</CardTitle></CardHeader>
          <CardContent>
            <EmptyState v-if="!data.trend.length" :title="t('dashboard.manager.nothingClosed')" data-testid="trend-empty" />
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
            <div class="flex h-40 items-end gap-1.5">
              <div
                v-for="d in data.forecast"
                :key="d.date"
                class="flex h-full min-w-0 flex-1 flex-col items-center justify-end"
                data-testid="forecast-day"
                :title="`${formatDate(d.date)}: ${d.rooms_booked}/${d.rooms_sellable}`"
              >
                <span class="block min-h-0.5 w-full rounded-t-sm bg-primary/50" data-testid="forecast-bar" :style="{ height: bar(d.occupancy_percent) }" />
                <small class="mt-1 text-[10px] text-muted-foreground">{{ shortDate(d.date) }}</small>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    </template>
  </section>
</template>
