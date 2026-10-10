<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { ChevronLeft } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { TapeBooking, TapeChart } from '@/api/types'
import PageHeader from '@/components/app/PageHeader.vue'
import { statusLegend, statusSwatch } from '@/components/app/statusMap'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { i18n, t } from '@/i18n'
import { cn } from '@/lib/utils'
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
// A calendar date has one weekday whatever the time zone: read it in UTC, in the language of the page.
const weekday = (d: string) => new Date(`${d}T00:00:00Z`).toLocaleDateString(i18n.global.locale.value, { weekday: 'short', timeZone: 'UTC' })
const isWeekend = (d: string) => [0, 6].includes(new Date(`${d}T00:00:00Z`).getUTCDay())

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

// A booking that is still to come is a light blue bar with a blue outline; a guest in the house is the same blue solid, so the two differ in weight and not only in hue. The legend is the same map.
const legend = computed(() => [
  ...statusLegend('reservation', ['CONFIRMED', 'CHECKED_IN']).map((e) => ({ ...e, label: t(e.status === 'CHECKED_IN' ? 'tapeChart.legendCheckedIn' : 'tapeChart.legendConfirmed') })),
  { status: 'BLOCKED', variant: 'ooo', swatch: statusSwatch('block', 'OOO').swatch, label: t('tapeChart.legendBlocked') },
])

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
  <PageHeader :title="t('tapeChart.title')">
    <template #actions>
      <Button variant="outline" size="sm" @click="shift(-7)"><ChevronLeft />{{ t('tapeChart.week') }}</Button>
      <Button variant="outline" size="sm" @click="start = businessDate; load()">{{ t('tapeChart.businessDate') }}</Button>
      <Button variant="outline" size="sm" @click="shift(7)">{{ t('tapeChart.week') }} &rarr;</Button>
      <NativeSelect v-model.number="days" class="w-28" :aria-label="t('tapeChart.daysShown')" @change="load">
        <option :value="14">{{ t('tapeChart.days', { n: 14 }) }}</option>
        <option :value="28">{{ t('tapeChart.days', { n: 28 }) }}</option>
      </NativeSelect>
      <Button as-child variant="outline" size="sm"><RouterLink to="/reservations">{{ t('tapeChart.reservations') }}</RouterLink></Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="property.currentId === null" class="muted">{{ t('tapeChart.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('tapeChart.noAccess') }}</p>

  <template v-else-if="chart">
    <ul class="m-0 mb-3 flex list-none flex-wrap gap-4 p-0 text-xs text-muted-foreground" data-testid="legend">
      <li v-for="l in legend" :key="l.status" class="flex items-center gap-1.5" :data-legend="l.status"><span :class="cn('h-3 w-5 rounded-sm border', l.swatch)" aria-hidden="true" />{{ l.label }}</li>
      <li class="flex items-center gap-1.5"><span class="h-3 w-5 rounded-sm bg-primary/10 ring-1 ring-primary/40" aria-hidden="true" />{{ t('tapeChart.legendToday') }}</li>
    </ul>

    <Card class="overflow-x-auto">
      <table class="w-full min-w-[720px] border-collapse text-xs" data-testid="tape">
        <thead>
          <tr>
            <th class="sticky left-0 z-10 min-w-24 border-b border-border bg-card" />
            <th
              v-for="d in dates"
              :key="d"
              :class="cn('min-w-11 border-b border-l border-border px-1 py-1.5 font-medium text-muted-foreground', isWeekend(d) && 'bg-muted/60', d === businessDate && 'bg-primary/10 text-primary')"
            >
              <span class="block text-[10px] uppercase">{{ weekday(d) }}</span>{{ dayLabel(d) }}
            </th>
          </tr>
        </thead>
        <tbody>
          <template v-for="(room, i) in chart.rooms" :key="room.room_id">
            <tr v-if="i === 0 || chart.rooms[i - 1]?.room_type_id !== room.room_type_id">
              <th :colspan="dates.length + 1" class="border-b border-t border-border bg-muted px-2 py-1 text-left text-xs font-semibold">{{ room.room_type_code }}</th>
            </tr>
            <tr :data-testid="`room-${room.room_number}`">
              <th scope="row" class="sticky left-0 z-10 border-b border-border bg-card px-2 py-1 text-left font-medium">{{ room.room_number }}</th>
              <td v-for="d in dates" :key="d" :class="cn('h-8 border-b border-l border-border p-0 text-center', isWeekend(d) && 'bg-muted/40', d === businessDate && 'bg-primary/10')">
                <template v-for="c in [cellAt(room.room_id, d)]" :key="d">
                  <RouterLink
                    v-if="c && c.kind === 'booking'"
                    :class="cn('block h-full overflow-hidden whitespace-nowrap border-y px-1 py-2 no-underline', statusSwatch('reservation', c.booking.status).fill, c.booking.status === 'CONFIRMED' && 'border-status-inhouse')"
                    :to="`/reservations/${c.booking.reservation_id}`"
                    :title="`${c.booking.confirmation_number} ${c.booking.guest_name ?? ''}`"
                    :data-testid="`bar-${room.room_number}-${d}`"
                  >
                    {{ c.booking.arrival_date === d || d === dates[0] ? (c.booking.guest_name || c.booking.confirmation_number) : '' }}
                  </RouterLink>
                  <span v-else-if="c" :class="cn('block border-y px-1 py-2', statusSwatch('block', c.type).fill)" :data-testid="`block-${room.room_number}-${d}`">{{ c.type }}</span>
                </template>
              </td>
            </tr>
          </template>
          <template v-for="u in chart.unassigned" :key="u.room_type_id">
            <tr><th :colspan="dates.length + 1" class="border-b border-t border-border bg-muted px-2 py-1 text-left text-xs font-semibold">{{ t('tapeChart.notAssigned') }} · {{ u.room_type_code }}</th></tr>
            <tr :data-testid="`unassigned-${u.room_type_code}`">
              <th scope="row" class="sticky left-0 z-10 border-b border-border bg-card px-2 py-1 text-left font-normal text-muted-foreground">{{ t('tapeChart.bookings', { n: u.bookings.length }) }}</th>
              <td v-for="d in dates" :key="d" :class="cn('h-8 border-b border-l border-border p-0 text-center', isWeekend(d) && 'bg-muted/40', d === businessDate && 'bg-primary/10')">
                <span v-if="unassignedAt(u.room_type_id, d).length" class="font-semibold" :data-testid="`count-${u.room_type_code}-${d}`">{{ unassignedAt(u.room_type_id, d).length }}</span>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </Card>
  </template>
</template>
