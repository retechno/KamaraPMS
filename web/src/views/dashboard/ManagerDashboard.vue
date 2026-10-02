<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Dashboard } from '@/api/types'
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
    { key: 'occupancy', label: 'Occupancy %', now: d.month_to_date.occupancy_percent, before: d.previous_month.occupancy_percent },
    { key: 'adr', label: 'ADR', now: d.month_to_date.adr, before: d.previous_month.adr },
    { key: 'revpar', label: 'RevPAR', now: d.month_to_date.revpar, before: d.previous_month.revpar },
    { key: 'revenue', label: 'Room revenue', now: d.month_to_date.room_revenue, before: d.previous_month.room_revenue },
  ]
  return rows.map((r) => ({ ...r, delta: delta(r.now, r.before) }))
})

const shortDate = (iso: string): string => iso.slice(5)
</script>

<template>
  <section v-if="allowed" class="dash" data-testid="manager-dashboard">
    <div class="head">
      <h2>Manager dashboard</h2>
      <span v-if="loadedAt" class="muted">Updated {{ loadedAt.toLocaleTimeString() }}</span>
      <button type="button" :disabled="busy" data-testid="dash-refresh" @click="load">{{ busy ? 'Loading…' : 'Refresh' }}</button>
    </div>
    <p v-if="error" class="alert" role="alert" data-testid="dash-error">{{ error.message }} <code>{{ error.code }}</code></p>

    <template v-if="data">
      <div class="kpis">
        <div class="kpi" data-testid="kpi-occupancy"><small>Occupancy today</small><b>{{ today?.occupancy_percent ?? '0.00' }}%</b><span class="muted">{{ today?.rooms.occupied ?? 0 }} of {{ today?.rooms.sellable ?? 0 }} rooms</span></div>
        <div class="kpi" data-testid="kpi-adr"><small>ADR</small><b>{{ today?.adr ?? '0' }}</b></div>
        <div class="kpi" data-testid="kpi-revpar"><small>RevPAR</small><b>{{ today?.revpar ?? '0' }}</b></div>
        <div class="kpi" data-testid="kpi-revenue"><small>Room revenue today</small><b>{{ today?.room_revenue.net ?? '0' }}</b></div>
        <div class="kpi" data-testid="kpi-arrivals">
          <small>Arrivals left</small><b>{{ data.movements.arrivals_expected }}</b>
          <span class="muted">{{ data.movements.arrivals_checked_in }} checked in</span>
        </div>
        <div class="kpi" data-testid="kpi-departures">
          <small>Departures left</small><b>{{ data.movements.departures_expected }}</b>
          <span class="muted">{{ data.movements.departures_checked_out }} checked out</span>
        </div>
        <div class="kpi" data-testid="kpi-inhouse">
          <small>In house</small><b>{{ data.movements.in_house }}</b>
          <span class="muted">owe {{ data.movements.in_house_balance }}</span>
        </div>
        <div class="kpi" data-testid="kpi-city-ledger">
          <small>Owed by companies</small><b>{{ today?.city_ledger.outstanding ?? '0' }}</b>
          <RouterLink to="/city-ledger">City ledger</RouterLink>
        </div>
      </div>

      <div class="grid">
        <div class="panel" data-testid="housekeeping">
          <h3>Rooms</h3>
          <dl class="pairs">
            <div><dt>Clean</dt><dd>{{ data.rooms.clean }}</dd></div>
            <div><dt>Inspected</dt><dd>{{ data.rooms.inspected }}</dd></div>
            <div><dt>Being cleaned</dt><dd>{{ data.rooms.cleaning }}</dd></div>
            <div><dt>Dirty</dt><dd :class="{ warn: data.rooms.dirty > 0 }">{{ data.rooms.dirty }}</dd></div>
            <div><dt>Out of order</dt><dd>{{ today?.rooms.out_of_order ?? 0 }}</dd></div>
          </dl>
        </div>

        <div class="panel" data-testid="month">
          <h3>Month so far <small class="muted">against the same days last month</small></h3>
          <p v-if="!data.month_to_date.days" class="muted" data-testid="month-empty">No day of this month is closed yet.</p>
          <table v-else class="list">
            <thead><tr><th></th><th class="num">This month</th><th class="num">Last month</th><th class="num">Change</th></tr></thead>
            <tbody>
              <tr v-for="r in comparison" :key="r.key" :data-testid="`month-${r.key}`">
                <td>{{ r.label }}</td><td class="num">{{ r.now }}</td><td class="num">{{ r.before }}</td>
                <td class="num"><span v-if="r.delta" :class="r.delta.up ? 'up' : 'down'">{{ r.delta.text }}</span><span v-else class="muted">–</span></td>
              </tr>
            </tbody>
          </table>
        </div>

        <div v-if="today?.payments_by_method.length" class="panel" data-testid="payments">
          <h3>Received today</h3>
          <dl class="pairs">
            <div v-for="m in today.payments_by_method" :key="m.method"><dt>{{ m.method }}</dt><dd>{{ m.net }}</dd></div>
          </dl>
        </div>
      </div>

      <div class="panel" data-testid="trend">
        <h3>Occupancy, last {{ data.trend.length }} closed days</h3>
        <p v-if="!data.trend.length" class="muted" data-testid="trend-empty">Nothing is closed yet.</p>
        <div v-else class="bars">
          <div v-for="d in data.trend" :key="d.business_date" class="col" :title="`${d.business_date}: ${d.occupancy_percent}% · ADR ${d.adr} · RevPAR ${d.revpar}`">
            <span class="fill" :style="{ height: bar(d.occupancy_percent) }"></span>
            <small>{{ shortDate(d.business_date) }}</small>
          </div>
        </div>
      </div>

      <div class="panel" data-testid="forecast">
        <h3>Next {{ data.forecast.length }} nights <small class="muted">rooms held by reservations and stays</small></h3>
        <div class="bars">
          <div v-for="d in data.forecast" :key="d.date" class="col" :title="`${d.date}: ${d.rooms_booked} of ${d.rooms_sellable} rooms`">
            <span class="fill ahead" :style="{ height: bar(d.occupancy_percent) }"></span>
            <small>{{ shortDate(d.date) }}</small>
          </div>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.dash {
  margin-bottom: 16px;
}
.head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}
.head h2 {
  margin: 0;
  font-size: 18px;
  flex: 1;
}
.kpis {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(170px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}
.kpi,
.panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 14px 16px;
}
.kpi {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.kpi small {
  color: var(--muted, #6b7280);
}
.kpi b {
  font-size: 22px;
  letter-spacing: -0.02em;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 12px;
  margin-bottom: 12px;
}
.panel {
  margin-bottom: 12px;
}
.grid .panel {
  margin-bottom: 0;
}
.panel h3 {
  margin: 0 0 10px;
  font-size: 15px;
}
.pairs > div {
  display: flex;
  justify-content: space-between;
  padding: 5px 0;
  border-top: 1px solid var(--border);
}
.pairs > div:first-child {
  border-top: 0;
}
.pairs dd {
  margin: 0;
  font-weight: 600;
}
.num {
  text-align: right;
  white-space: nowrap;
}
.up {
  color: var(--success, #15803d);
}
.down,
.warn {
  color: var(--danger, #b91c1c);
}
.bars {
  display: flex;
  align-items: flex-end;
  gap: 6px;
  height: 140px;
}
.col {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  align-items: center;
  height: 100%;
  min-width: 0;
}
.fill {
  display: block;
  width: 100%;
  min-height: 2px;
  background: var(--primary, #2563eb);
  border-radius: 3px 3px 0 0;
}
.fill.ahead {
  opacity: 0.55;
}
.col small {
  margin-top: 4px;
  font-size: 10px;
  color: var(--muted, #6b7280);
}
</style>
