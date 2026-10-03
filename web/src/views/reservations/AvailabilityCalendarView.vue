<script setup lang="ts">
import { ChevronLeft } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AvailabilityCalendar, BedType } from '@/api/types'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { i18n, t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { windowDates } from '@/utils/blocks'
import { addDays } from '@/utils/dates'

interface Night {
  date: string
  sellable: number
  blocked: number
  held: number
  available: number
  occupancy_percent: string
}

const auth = useAuthStore()
const property = usePropertyStore()

const days = ref(14)
const start = ref('')
const calendar = ref<AvailabilityCalendar | null>(null)
const error = ref<ApiError | null>(null)
const beds = ref<BedType[]>([])
const bedTypeId = ref('') // '' = every bed

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
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/calendar', {
      params: { path: { propertyId }, query: { from: start.value, to: addDays(start.value, days.value), bed_type_id: bedTypeId.value ? Number(bedTypeId.value) : undefined } },
    })
    calendar.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

// The beds to filter by. Optional: without them the calendar still works for every bed.
async function loadBeds(): Promise<void> {
  const propertyId = property.currentId
  beds.value = []
  if (propertyId === null || !canRead.value) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/bed-types', { params: { path: { propertyId } } })
    beds.value = data?.data ?? []
  } catch {
    beds.value = []
  }
}

function shift(n: number): void {
  start.value = addDays(start.value, n)
  void load()
}

function tooltip(n: Night): string {
  return t('availabilityCalendar.cellTitle', { date: n.date, sellable: n.sellable, held: n.held, blocked: n.blocked, percent: n.occupancy_percent })
}

/** Colour of a night: oversold is red, a full type is amber, the rest is neutral. */
function tone(n: Night): string {
  if (n.available < 0) return 'bg-destructive/15 font-semibold text-destructive'
  if (n.available === 0) return 'bg-warning/20 font-medium'
  return ''
}

watch(() => property.currentId, () => {
  start.value = ''
  calendar.value = null
  bedTypeId.value = ''
  void load()
  void loadBeds()
}, { immediate: true })
watch(businessDate, () => {
  if (!calendar.value) void load()
})
</script>

<template>
  <PageHeader :title="t('availabilityCalendar.title')">
    <template #actions>
      <Button variant="outline" size="sm" @click="shift(-7)"><ChevronLeft />{{ t('availabilityCalendar.week') }}</Button>
      <Button variant="outline" size="sm" @click="start = businessDate; load()">{{ t('availabilityCalendar.businessDate') }}</Button>
      <Button variant="outline" size="sm" @click="shift(7)">{{ t('availabilityCalendar.week') }} &rarr;</Button>
      <NativeSelect v-if="beds.length" v-model="bedTypeId" name="bed_type_id" class="w-36" :aria-label="t('availabilityCalendar.bedType')" @change="load">
        <option value="">{{ t('availabilityCalendar.allBeds') }}</option>
        <option v-for="b in beds" :key="b.id" :value="String(b.id)">{{ b.name }}</option>
      </NativeSelect>
      <NativeSelect v-model.number="days" class="w-28" :aria-label="t('availabilityCalendar.daysShown')" @change="load">
        <option :value="14">{{ t('availabilityCalendar.days', { n: 14 }) }}</option>
        <option :value="28">{{ t('availabilityCalendar.days', { n: 28 }) }}</option>
      </NativeSelect>
      <Button as-child variant="ghost" size="sm"><RouterLink to="/reservations/tape">{{ t('availabilityCalendar.tapeChart') }}</RouterLink></Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('availabilityCalendar.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('availabilityCalendar.noAccess') }}</p>

  <template v-else-if="calendar">
    <p class="mb-3 text-xs text-muted-foreground" data-testid="legend">{{ t('availabilityCalendar.legend') }}</p>
    <p v-if="bedTypeId" class="notice" data-testid="bed-note">{{ t('availabilityCalendar.bedNote') }}</p>
    <p v-if="calendar.room_types.length === 0" class="muted" data-testid="empty">{{ t('availabilityCalendar.empty') }}</p>

    <Card v-else class="overflow-x-auto">
      <table class="w-full min-w-[720px] border-collapse text-xs" data-testid="calendar">
        <thead>
          <tr>
            <th class="sticky left-0 z-10 min-w-32 border-b border-border bg-card" />
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
          <tr v-for="ty in calendar.room_types" :key="ty.room_type_id">
            <th class="sticky left-0 z-10 border-b border-border bg-card px-2 py-1.5 text-left font-medium">
              {{ ty.code }}
              <span class="block text-[10px] font-normal text-muted-foreground">{{ ty.name }} &middot; {{ t('availabilityCalendar.rooms', { n: ty.rooms_total }) }}</span>
            </th>
            <td
              v-for="n in ty.nights"
              :key="n.date"
              :title="tooltip(n)"
              :data-testid="`cell-${ty.code}-${n.date}`"
              :class="cn('border-b border-l border-border px-1 py-1.5 text-center tabular-nums', tone(n))"
            >
              {{ n.available }}
            </td>
          </tr>
        </tbody>
        <tfoot>
          <tr>
            <th class="sticky left-0 z-10 bg-card px-2 py-1.5 text-left font-semibold">{{ t('availabilityCalendar.total') }}</th>
            <td
              v-for="n in calendar.totals"
              :key="n.date"
              :title="tooltip(n)"
              :data-testid="`totals-${n.date}`"
              :class="cn('border-l border-border px-1 py-1.5 text-center font-semibold tabular-nums', tone(n))"
            >
              {{ n.available }}
              <span class="block text-[10px] font-normal text-muted-foreground" :data-testid="`occupancy-${n.date}`">{{ n.occupancy_percent }}%</span>
            </td>
          </tr>
        </tfoot>
      </table>
    </Card>
  </template>
</template>
