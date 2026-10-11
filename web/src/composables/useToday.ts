import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { Arrival, AvailabilityCalendar, CityLedgerAccount, HousekeepingBoardRoom, InHouseRow } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'

/** The nights the chart shows, today first. */
export const NIGHTS = 14
/** How often the page reads again by itself, and no oftener. */
export const REFRESH_EVERY_MS = 2 * 60 * 1000

export type Night = AvailabilityCalendar['totals'][number]

/** One part of the page: what it read, whether it has read once, and whether the last read failed. A failed part says so and keeps what it had; the others carry on. */
interface Part<T> {
  data: T
  loaded: boolean
  failed: boolean
}
const part = <T>(empty: T): Part<T> => ({ data: empty, loaded: false, failed: false })

/**
 * The data of the "Today" page, for everyone on the staff and without money (the dashboard of the API is for the managers). Each part has its own request and its own permission:
 * the nights of the calendar (occupancy tonight and the chart), the arrivals still to come and how many have come, the stays that leave by today, the housekeeping board, and the company
 * accounts. A part that fails is marked and the rest is shown. The page reads again every two minutes and when the tab comes back, but not while `paused()` says a sheet is open (a
 * check-in being filled in must not be moved under the clerk's hands); it never starts a read while another is on its way.
 */
export function useToday(paused: () => boolean = () => false) {
  const auth = useAuthStore()
  const property = usePropertyStore()

  const nights = ref(part<Night[]>([]))
  const arrivals = ref(part<Arrival[]>([]))
  const arrivedCount = ref(0)
  const departures = ref(part<InHouseRow[]>([]))
  const rooms = ref(part<HousekeepingBoardRoom[]>([]))
  const accounts = ref(part<CityLedgerAccount[]>([]))
  const refreshing = ref(false)
  const updatedAt = ref<Date | null>(null)

  const pid = computed(() => property.currentId)
  const businessDate = computed(() => property.clock?.business_date ?? '')
  const can = (permission: string): boolean => pid.value !== null && auth.can(permission, pid.value)
  const canFront = computed(() => can('reservation.read'))
  const canBoard = computed(() => can('housekeeping.read') || can('reservation.read'))
  const canLedger = computed(() => can('cityledger.read'))

  // Only the latest read writes: a slow answer to an older read (another property, a button pressed twice) must not replace a newer one.
  let latest = 0

  async function run<T>(mine: number, target: { value: Part<T> }, ask: () => Promise<T>): Promise<void> {
    try {
      const value = await ask()
      if (mine !== latest) return
      target.value = { data: value, loaded: true, failed: false }
    } catch {
      if (mine !== latest) return
      target.value = { ...target.value, failed: true }
    }
  }

  async function reload(): Promise<void> {
    const propertyId = pid.value
    const day = businessDate.value
    if (propertyId === null || !day) return
    const mine = ++latest
    refreshing.value = true
    const path = { propertyId }
    const jobs: Promise<void>[] = []
    if (canFront.value) {
      jobs.push(
        run(mine, nights, async () => {
          const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/calendar', { params: { path, query: { from: day, to: addDays(day, NIGHTS) } } })
          if (!data) throw new Error('no calendar')
          return data.totals
        }),
        run(mine, arrivals, async () => {
          // The two reads are one part: the arrivals to come, and (for "n of m") how many have come already.
          const [left, done] = await Promise.all([
            api.GET('/api/v1/properties/{propertyId}/arrivals', { params: { path, query: { status: 'CONFIRMED' } } }),
            api.GET('/api/v1/properties/{propertyId}/arrivals', { params: { path, query: { status: 'CHECKED_IN' } } }),
          ])
          if (!left.data || !done.data) throw new Error('no arrivals')
          if (mine === latest) arrivedCount.value = done.data.data.length
          return left.data.data
        }),
        run(mine, departures, async () => {
          const { data } = await api.GET('/api/v1/properties/{propertyId}/stays/in-house', { params: { path, query: { limit: 50, departure_until: day } } })
          if (!data) throw new Error('no departures')
          return data.data
        }),
      )
    }
    if (canBoard.value) {
      jobs.push(
        run(mine, rooms, async () => {
          const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping', { params: { path } })
          if (!data) throw new Error('no board')
          return data.data
        }),
      )
    }
    if (canLedger.value) {
      jobs.push(
        run(mine, accounts, async () => {
          const { data } = await api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts', { params: { path, query: { limit: 200 } } })
          if (!data) throw new Error('no accounts')
          return data.data
        }),
      )
    }
    await Promise.all(jobs)
    if (mine !== latest) return
    refreshing.value = false
    updatedAt.value = new Date()
  }

  // A different property (or the day turning over) starts again from nothing: what was read for the other one is not shown.
  watch(() => [pid.value, businessDate.value], () => {
    nights.value = part([])
    arrivals.value = part([])
    arrivedCount.value = 0
    departures.value = part([])
    rooms.value = part([])
    accounts.value = part([])
    void reload()
  }, { immediate: true })

  function tick(): void {
    if (paused() || refreshing.value || (typeof document !== 'undefined' && document.hidden)) return
    void reload()
  }
  function visibility(): void {
    if (typeof document !== 'undefined' && document.visibilityState === 'visible') tick()
  }
  const timer = setInterval(tick, REFRESH_EVERY_MS)
  document.addEventListener('visibilitychange', visibility)
  onBeforeUnmount(() => {
    clearInterval(timer)
    document.removeEventListener('visibilitychange', visibility)
    latest++ // an answer that comes after the page is gone writes nothing
  })

  return { nights, arrivals, arrivedCount, departures, rooms, accounts, refreshing, updatedAt, reload, canFront, canBoard, canLedger, businessDate }
}
