<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { AvailabilityCalendar } from '@/api/types'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { i18n, t } from '@/i18n'
import { cn } from '@/lib/utils'
import { addDays } from '@/utils/dates'

/**
 * The next 14 nights of every room type from the arrival, under the search of a new reservation: what is left per night, so the
 * clerk sees the pinch before searching. It reads the calendar of the availability page (advisory, like the search); the nights
 * of the stay are shaded. Numbers only: the heading says what they are.
 */
const props = defineProps<{ propertyId: number; arrival: string; departure: string; businessDate: string }>()

const NIGHTS = 14
const calendar = ref<AvailabilityCalendar | null>(null)
const failed = ref(false)
let request = 0

const validArrival = computed(() => /^\d{4}-\d{2}-\d{2}$/.test(props.arrival) && props.businessDate !== '' && props.arrival >= props.businessDate)
const dates = computed(() => (validArrival.value ? Array.from({ length: NIGHTS }, (_, i) => addDays(props.arrival, i)) : []))
const staysOn = (d: string) => d >= props.arrival && d < props.departure
const cut = computed(() => validArrival.value && props.departure > addDays(props.arrival, NIGHTS))

const dayLabel = (d: string) => `${d.slice(8)}/${d.slice(5, 7)}`
// A calendar date has one weekday whatever the time zone: read it in UTC, in the language of the page.
const weekday = (d: string) => new Date(`${d}T00:00:00Z`).toLocaleDateString(i18n.global.locale.value, { weekday: 'short', timeZone: 'UTC' })

/** The colour of a cell: oversold is red, a full type is amber, the rest is neutral (the number says the same without it). */
function tone(available: number): string {
  if (available < 0) return 'bg-destructive/15 font-semibold text-destructive'
  if (available === 0) return 'bg-warning/20 font-medium'
  return ''
}

async function load(): Promise<void> {
  const mine = ++request
  if (!validArrival.value) {
    calendar.value = null
    failed.value = false
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/calendar', {
      params: { path: { propertyId: props.propertyId }, query: { from: props.arrival, to: addDays(props.arrival, NIGHTS) } },
    })
    if (mine !== request) return // a later arrival has been asked since
    calendar.value = data ?? null
    failed.value = false
  } catch {
    if (mine !== request) return
    calendar.value = null
    failed.value = true
  }
}

// The window depends on the arrival (and the property), not on the departure or the party.
watch(() => [props.propertyId, props.arrival, props.businessDate], () => { void load() }, { immediate: true })
onBeforeUnmount(() => { request++ })
</script>

<template>
  <Card v-if="validArrival" class="mb-4" data-testid="availability-strip">
    <CardHeader><CardTitle>{{ t('newReservation.stripHeading', { n: NIGHTS }) }}</CardTitle></CardHeader>
    <CardContent>
      <p v-if="failed" class="muted m-0" role="status" data-testid="strip-failed">{{ t('newReservation.stripFailed') }}</p>
      <template v-else-if="calendar">
        <p v-if="calendar.room_types.length === 0" class="muted m-0" data-testid="strip-empty">{{ t('newReservation.stripEmpty') }}</p>
        <div v-else class="relative overflow-x-auto" data-testid="strip-scroll"><!-- relative: the sr-only text of the nights out of view is absolute and would otherwise widen the page -->
          <table class="w-full min-w-[640px] border-collapse text-[13px]">
            <caption class="sr-only">{{ t('newReservation.stripHeading', { n: NIGHTS }) }}</caption>
            <thead>
              <tr>
                <th scope="col" class="sticky left-0 z-10 w-16 border-b border-border bg-card px-2 py-1.5 text-left font-medium text-muted-foreground">{{ t('newReservation.roomType') }}</th>
                <th
                  v-for="d in dates" :key="d" scope="col" :data-strip-day="d"
                  :class="cn('border-b border-l border-border px-1 py-1.5 text-center font-medium text-muted-foreground', staysOn(d) && 'border-t-2 border-t-primary bg-primary/10', d === businessDate && 'text-primary')"
                >
                  <span class="block text-[12px] uppercase">{{ weekday(d) }}</span>{{ dayLabel(d) }}<span v-if="staysOn(d)" class="sr-only"> {{ t('newReservation.stripStay') }}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="ty in calendar.room_types" :key="ty.code" :data-testid="`strip-${ty.code}`">
                <th scope="row" class="sticky left-0 z-10 border-b border-border bg-card px-2 py-1 text-left font-semibold" :title="ty.name">{{ ty.code }}</th>
                <td
                  v-for="n in ty.nights.slice(0, NIGHTS)" :key="n.date" :data-strip-cell="`${ty.code}-${n.date}`" :data-available="n.available"
                  :title="t('availabilityCalendar.cellTitle', { date: n.date, sellable: n.sellable, held: n.held, blocked: n.blocked, percent: n.occupancy_percent })"
                  :class="cn('border-b border-l border-border px-1 py-1 text-center tabular-nums', staysOn(n.date) && 'bg-primary/10', tone(n.available))"
                >{{ n.available }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="m-0 mt-2 text-[13px] text-muted-foreground">{{ cut ? `${t('newReservation.stripNote')} ${t('newReservation.stripCut', { n: NIGHTS })}` : t('newReservation.stripNote') }}</p>
      </template>
    </CardContent>
  </Card>
</template>
