<script setup lang="ts">
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
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

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

// A bar is as tall as the occupancy it shows (0 to 100%).
const bar = (percent: string): string => `${Math.min(100, Math.max(0, Number(percent)))}%`

// The change of a figure against the same days of the month before: nothing to compare with when that was zero.
function delta(now: string, before: string): { text: string; up: boolean } | null {
  const a = Number(now)
  const b = Number(before)
  if (!b) return null
  const pct = ((a - b) / b) * 100
  return { text: `${pct >= 0 ? '▲' : '▼'} ${Math.abs(pct).toFixed(1)}%`, up: pct >= 0 }
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
  return rows.map((r) => ({ ...r, delta: delta(r.now, r.before) }))
})

const shortDate = (iso: string): string => iso.slice(5)
const rooms = computed(() => {
  const r = data.value?.rooms
  if (!r) return []
  return [
    { key: 'clean', label: t('dashboard.manager.clean'), value: r.clean, dot: 'bg-success' },
    { key: 'inspected', label: t('dashboard.manager.inspected'), value: r.inspected, dot: 'bg-primary' },
    { key: 'cleaning', label: t('dashboard.manager.beingCleaned'), value: r.cleaning, dot: 'bg-muted-foreground' },
    { key: 'dirty', label: t('dashboard.manager.dirty'), value: r.dirty, dot: 'bg-warning', warn: r.dirty > 0 },
    { key: 'ooo', label: t('dashboard.manager.outOfOrder'), value: today.value?.rooms.out_of_order ?? 0, dot: 'bg-destructive' },
  ]
})
</script>

<template>
  <section v-if="allowed" class="mb-6" data-testid="manager-dashboard">
    <div class="mb-3 flex flex-wrap items-center gap-3">
      <h2 class="m-0 flex-1 text-lg font-semibold">{{ t('dashboard.manager.title') }}</h2>
      <span v-if="loadedAt" class="text-sm text-muted-foreground">{{ t('dashboard.manager.updated', { time: loadedAt.toLocaleTimeString() }) }}</span>
      <Button variant="outline" size="sm" :disabled="busy" data-testid="dash-refresh" @click="load">
        <RefreshCw :class="busy && 'animate-spin'" />
        {{ busy ? t('common.loading') : t('common.refresh') }}
      </Button>
    </div>
    <p v-if="error" class="alert" role="alert" data-testid="dash-error">{{ error.message }} <code>{{ error.code }}</code></p>

    <template v-if="data">
      <div class="mb-4 grid grid-cols-[repeat(auto-fill,minmax(10.5rem,1fr))] gap-3">
        <KpiCard
          data-testid="kpi-occupancy"
          :label="t('dashboard.manager.occupancyToday')"
          :value="`${today?.occupancy_percent ?? '0.00'}%`"
          :hint="t('dashboard.manager.roomsOf', { occupied: today?.rooms.occupied ?? 0, sellable: today?.rooms.sellable ?? 0 })"
          :icon="Percent"
        />
        <KpiCard data-testid="kpi-adr" :label="t('dashboard.manager.adr')" :value="today?.adr ?? '0'" :icon="TrendingUp" />
        <KpiCard data-testid="kpi-revpar" :label="t('dashboard.manager.revpar')" :value="today?.revpar ?? '0'" :icon="BedDouble" />
        <KpiCard data-testid="kpi-revenue" :label="t('dashboard.manager.revenueToday')" :value="today?.room_revenue.net ?? '0'" :icon="Wallet" />
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
          :hint="t('dashboard.manager.owe', { amount: data.movements.in_house_balance })"
          :icon="Users"
        />
        <KpiCard data-testid="kpi-city-ledger" :label="t('dashboard.manager.owedByCompanies')" :value="today?.city_ledger.outstanding ?? '0'" :icon="Landmark">
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
                <dd :class="cn('m-0 font-semibold', r.warn && 'text-warning')">{{ r.value }}</dd>
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
                  <td class="py-1.5 text-right font-medium">{{ r.now }}</td>
                  <td class="py-1.5 text-right text-muted-foreground">{{ r.before }}</td>
                  <td class="py-1.5 text-right">
                    <span v-if="r.delta" :class="cn('inline-flex items-center gap-0.5', r.delta.up ? 'text-success' : 'text-destructive')">
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
                <dt>{{ m.method }}</dt>
                <dd class="m-0 font-semibold">{{ m.net }}</dd>
              </div>
            </dl>
          </CardContent>
        </Card>
      </div>

      <div class="grid gap-4 lg:grid-cols-2">
        <Card data-testid="trend">
          <CardHeader><CardTitle>{{ t('dashboard.manager.occupancyTrend', { n: data.trend.length }) }}</CardTitle></CardHeader>
          <CardContent>
            <EmptyState v-if="!data.trend.length" :title="t('dashboard.manager.nothingClosed')" data-testid="trend-empty" />
            <div v-else class="flex h-40 items-end gap-1.5">
              <div
                v-for="d in data.trend"
                :key="d.business_date"
                class="flex h-full min-w-0 flex-1 flex-col items-center justify-end"
                data-testid="trend-day"
                :title="`${d.business_date}: ${d.occupancy_percent}% · ${t('dashboard.manager.adr')} ${d.adr} · ${t('dashboard.manager.revpar')} ${d.revpar}`"
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
                :title="`${d.date}: ${d.rooms_booked}/${d.rooms_sellable}`"
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
