<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { TapeBooking, TapeChart } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { windowDates } from '@/utils/blocks'
import { addDays } from '@/utils/dates'

const auth = useAuthStore()
const property = usePropertyStore()

const days = ref(14)
const start = ref('')
const chart = ref<TapeChart | null>(null)
const error = ref<ApiError | null>(null)

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')
const dates = computed(() => (start.value ? windowDates(start.value, days.value) : []))
const dayLabel = (d: string) => `${d.slice(8)}/${d.slice(5, 7)}`

async function load(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  if (!start.value) start.value = businessDate.value
  if (!start.value) return // the business date is still loading; the watcher below retries
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tape-chart', {
      params: { path: { propertyId }, query: { from: start.value, to: addDays(start.value, days.value) } },
    })
    chart.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function shift(n: number): void {
  start.value = addDays(start.value, n)
  void load()
}

type Cell = { kind: 'booking'; booking: TapeBooking } | { kind: 'block'; type: string } | null

/** What occupies a room on a night: a booking (arrival night to the night before departure) or a block. */
function cellAt(roomId: number, date: string): Cell {
  const row = chart.value?.rooms.find((r) => r.room_id === roomId)
  if (!row) return null
  const booking = row.bookings.find((b) => b.arrival_date <= date && date < b.departure_date)
  if (booking) return { kind: 'booking', booking }
  const block = row.blocks.find((b) => b.start_date <= date && date < b.end_date)
  return block ? { kind: 'block', type: block.block_type } : null
}

/** Rooms of a type without a specific room on a night. */
function unassignedAt(typeId: number, date: string): TapeBooking[] {
  const group = chart.value?.unassigned.find((u) => u.room_type_id === typeId)
  return (group?.bookings ?? []).filter((b) => b.arrival_date <= date && date < b.departure_date)
}

watch(() => property.currentId, () => {
  start.value = ''
  chart.value = null
  void load()
}, { immediate: true })
watch(businessDate, () => {
  if (!chart.value) void load()
})
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Tape chart</h1>
    <div class="nav">
      <button type="button" @click="shift(-7)">&larr; Week</button>
      <button type="button" @click="start = businessDate; load()">Business date</button>
      <button type="button" @click="shift(7)">Week &rarr;</button>
      <select v-model.number="days" aria-label="Days shown" @change="load">
        <option :value="14">14 days</option>
        <option :value="28">28 days</option>
      </select>
      <RouterLink to="/reservations">Reservations</RouterLink>
    </div>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property does not allow viewing reservations.</p>

  <section v-else-if="chart" class="card scroll">
    <table class="tape" data-testid="tape">
      <thead>
        <tr>
          <th />
          <th v-for="d in dates" :key="d" :class="{ today: d === businessDate }">{{ dayLabel(d) }}</th>
        </tr>
      </thead>
      <tbody>
        <template v-for="(room, i) in chart.rooms" :key="room.room_id">
          <tr v-if="i === 0 || chart.rooms[i - 1]?.room_type_id !== room.room_type_id" class="group">
            <th :colspan="dates.length + 1">{{ room.room_type_code }}</th>
          </tr>
          <tr :data-testid="`room-${room.room_number}`">
            <th scope="row">{{ room.room_number }}</th>
            <td v-for="d in dates" :key="d" :class="{ today: d === businessDate }">
              <template v-for="c in [cellAt(room.room_id, d)]" :key="d">
                <RouterLink v-if="c && c.kind === 'booking'" class="bar" :class="c.booking.status.toLowerCase()" :to="`/reservations/${c.booking.reservation_id}`"
                  :title="`${c.booking.confirmation_number} ${c.booking.guest_name ?? ''}`" :data-testid="`bar-${room.room_number}-${d}`">
                  {{ c.booking.arrival_date === d || d === dates[0] ? (c.booking.guest_name || c.booking.confirmation_number) : '' }}
                </RouterLink>
                <span v-else-if="c" class="block" :data-testid="`block-${room.room_number}-${d}`">{{ c.type }}</span>
              </template>
            </td>
          </tr>
        </template>
        <template v-for="u in chart.unassigned" :key="u.room_type_id">
          <tr class="group"><th :colspan="dates.length + 1">Not yet assigned · {{ u.room_type_code }}</th></tr>
          <tr :data-testid="`unassigned-${u.room_type_code}`">
            <th scope="row">{{ u.bookings.length }} booking(s)</th>
            <td v-for="d in dates" :key="d" :class="{ today: d === businessDate }">
              <span v-if="unassignedAt(u.room_type_id, d).length" class="count" :data-testid="`count-${u.room_type_code}-${d}`">{{ unassignedAt(u.room_type_id, d).length }}</span>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </section>
</template>

<style scoped>
.nav {
  display: flex;
  gap: 8px;
  align-items: center;
}
.nav select {
  font: inherit;
  padding: 6px 8px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
}
.scroll {
  overflow-x: auto;
  padding: 8px;
}
.tape {
  border-collapse: collapse;
  font-size: 12px;
  min-width: 720px;
  width: 100%;
}
.tape th,
.tape td {
  border: 1px solid var(--border);
  padding: 0;
  height: 28px;
  min-width: 44px;
  text-align: center;
}
.tape thead th {
  color: var(--text-muted);
  padding: 4px;
}
.tape tbody th {
  padding: 4px 8px;
  text-align: left;
}
.group th {
  background: var(--accent-soft);
  font-size: 12px;
}
.today {
  background: var(--accent-soft);
}
.bar {
  display: block;
  height: 100%;
  padding: 6px 2px;
  background: #cfe3ff;
  color: inherit;
  text-decoration: none;
  overflow: hidden;
  white-space: nowrap;
}
.bar.checked_in {
  background: #cdeccd;
}
.block {
  display: block;
  padding: 6px 2px;
  background: #e5e5e5;
  color: var(--text-muted);
}
.count {
  font-weight: 600;
}
</style>
