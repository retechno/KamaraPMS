<script setup lang="ts">
import { AlertTriangle, BedDouble, CalendarPlus, DoorClosed, DoorOpen, MoonStar, Percent, RefreshCw } from 'lucide-vue-next'
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Arrival, CheckInResult, CheckOutResult, InHouseRow, StayDetail } from '@/api/types'
import KpiCard from '@/components/app/KpiCard.vue'
import NightsChart, { type NightBar } from '@/components/app/NightsChart.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import CheckOutWizard from '@/components/CheckOutWizard.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { toast } from '@/composables/useToast'
import { useToday } from '@/composables/useToday'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { useSystemStore, type ComponentStatus } from '@/stores/system'
import { arrivalActions } from '@/utils/arrivals'
import { formatBusinessDate, wallClock } from '@/utils/dates'
import { formatBalance, formatDate, formatPercent } from '@/utils/format'
import { greetingFor, listWithMore, overCreditLimit, arrivalsWithoutRoom, arrivalsWithUnreadyRoom, roomCounts } from '@/utils/today'
import { zoneTime } from '@/utils/zoneTime'
import CheckInPanel from '@/views/frontdesk/CheckInPanel.vue'
import TodayCard from '@/views/home/TodayCard.vue'

/**
 * "Today", the page everyone on the staff opens first: what the desk has to do now, and no money (the figures of the managers are on the Performance page). A strip of four figures,
 * three columns (arrivals, departures, what needs attention) with at most five rows each and a link to the whole list, and the occupancy of the nights ahead. The buttons of a row open
 * the check-in and the check-out that the front desk already has, in a sheet; nothing here is a new way to check in. The page reads again by itself (see `useToday`).
 */
const ROWS = 5

const auth = useAuthStore()
const property = usePropertyStore()
const system = useSystemStore()
// The status of the API and the database is for the administrator: the card and the check are not made for other staff.
onMounted(() => {
  if (auth.isAdmin) void system.check()
})

const pid = computed(() => property.currentId)
const can = (permission: string) => pid.value !== null && auth.can(permission, pid.value)

// The check-in and the check-out are filled in in a sheet: while one is open the page does not read again by itself, so nothing moves under the clerk's hands.
const checkIn = ref<Arrival | null>(null)
const checkOut = ref<{ row: InHouseRow; detail: StayDetail | null; loading: boolean } | null>(null)
const today = useToday(() => checkIn.value !== null || checkOut.value !== null)

const checkInOpen = computed({
  get: () => checkIn.value !== null,
  set: (v: boolean) => {
    if (!v) checkIn.value = null
  },
})
const checkOutOpen = computed({
  get: () => !!checkOut.value?.detail,
  set: (v: boolean) => {
    if (!v) checkOut.value = null
  },
})

async function checkedIn(result: CheckInResult): Promise<void> {
  checkIn.value = null
  toast.success(t('frontDesk.arrivals.checkedIn', { stay: result.stay.stay_number, room: result.stay_room.room_number }))
  await today.reload()
}

async function openCheckOut(row: InHouseRow): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  checkOut.value = { row, detail: null, loading: true }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/stays/{id}', { params: { path: { propertyId, id: row.id } } })
    if (!checkOut.value || checkOut.value.row.id !== row.id) return // closed or replaced while the stay was on its way
    if (!data) throw new Error('no stay')
    checkOut.value = { row, detail: data, loading: false }
  } catch (e) {
    checkOut.value = null
    toast.error(e instanceof ApiError ? e.message : t('today.stayNotLoaded'))
  }
}

async function checkedOut(result: CheckOutResult): Promise<void> {
  checkOut.value = null
  toast.success(t('stay.checkedOut', { n: result.posted_room_charges.length }))
  await today.reload()
}

// ---- the strip
const tonight = computed(() => today.nights.value.data[0] ?? null)
const counts = computed(() => roomCounts(today.rooms.value.data))
const arrivalsLeft = computed(() => today.arrivals.value.data)
const departuresLeft = computed(() => today.departures.value.data)
const arrivalsTotal = computed(() => arrivalsLeft.value.length + today.arrivedCount.value)

// ---- the greeting and what the day holds
const clockHour = computed(() => {
  const c = property.clock ? wallClock(property.clock.property_local_time) : null
  return c ? Number(c.time.slice(0, 2)) : null
})
const greeting = computed(() => {
  if (clockHour.value === null) return ''
  const word = t(`today.greeting${greetingFor(clockHour.value).replace(/^./, (c) => c.toUpperCase())}` as never)
  const first = auth.displayName.trim().split(/\s+/)[0] ?? ''
  return first ? t('today.greetingLine', { greeting: word, name: first }) : t('today.greetingNoName', { greeting: word })
})
const summary = computed(() => {
  const parts: string[] = []
  if (today.arrivals.value.loaded) parts.push(t('today.summaryArrivals', { n: arrivalsLeft.value.length }, arrivalsLeft.value.length))
  if (today.departures.value.loaded) parts.push(t('today.summaryDepartures', { n: departuresLeft.value.length }, departuresLeft.value.length))
  if (today.rooms.value.loaded) parts.push(t('today.summaryDirty', { n: counts.value.dirty }, counts.value.dirty))
  return parts.length ? `${parts.join(', ')}.` : ''
})
const description = computed(() => [greeting.value, summary.value].filter(Boolean).join(' '))

// ---- the columns
const arrivalRows = computed(() => arrivalsLeft.value.slice(0, ROWS))
const departureRows = computed(() => departuresLeft.value.slice(0, ROWS))
const canCheckIn = (a: Arrival): boolean => arrivalActions(a, can).includes('checkIn')
const balanceText = (r: InHouseRow): string => (r.balance.status === 'NO_FOLIO' ? t('today.noFolio') : formatBalance(r.balance.amount))

interface Attention {
  id: string
  tone: 'warn' | 'info'
  title: string
  detail: string
  to?: string
}
const auditAttention = computed<Attention | null>(() => {
  const c = property.clock
  if (!c || !property.current) return null
  const to = can('nightaudit.run') ? '/night-audit' : undefined
  const date = t('today.auditDate', { date: formatBusinessDate(c.business_date) })
  if (c.night_audit_overdue) return { id: 'audit', tone: 'warn', title: t('today.auditOverdue'), detail: date, to }
  if (c.night_audit_allowed) return { id: 'audit', tone: 'info', title: t('today.auditReady'), detail: date, to }
  return { id: 'audit', tone: 'info', title: t('today.auditOpens', { time: property.current.night_audit_earliest_time }), detail: date, to }
})
const attention = computed<Attention[]>(() => {
  const items: Attention[] = []
  const unready = arrivalsWithUnreadyRoom(arrivalsLeft.value)
  if (unready.length) items.push({ id: 'unready', tone: 'warn', title: t('today.unreadyRooms', { n: unready.length }, unready.length), detail: t('today.unreadyHint', { rooms: listWithMore(unready.map((a) => a.room_number ?? '')) }), to: '/housekeeping' })
  const noRoom = arrivalsWithoutRoom(arrivalsLeft.value)
  if (noRoom.length) items.push({ id: 'no-room', tone: 'warn', title: t('today.noRoomNumber', { n: noRoom.length }, noRoom.length), detail: t('today.noRoomHint', { types: listWithMore(noRoom.map((a) => a.room_type_code)) }), to: '/arrivals' })
  const over = overCreditLimit(today.accounts.value.data)
  if (over.length) items.push({ id: 'over-limit', tone: 'warn', title: t('today.overLimit', { n: over.length }, over.length), detail: listWithMore(over.map((a) => a.name), 3), to: '/city-ledger' })
  if (auditAttention.value) items.push(auditAttention.value)
  return items.slice(0, ROWS)
})
// The attention column has loaded when what it reads has: the arrivals always, the rest when this person may read them.
const attentionLoaded = computed(() => today.arrivals.value.loaded || !today.canFront.value)
const attentionFailed = computed(() => today.arrivals.value.failed || today.accounts.value.failed)

// ---- the nights ahead
const shortDate = (iso: string): string => formatDate(iso).replace(/ \d{4}$/, '')
const bars = computed<NightBar[]>(() =>
  today.nights.value.data.map((n) => ({
    key: n.date,
    percent: Number(n.occupancy_percent),
    label: shortDate(n.date),
    title: `${formatDate(n.date)}: ${n.held}/${n.sellable}`,
  })),
)

const updated = computed(() => (today.updatedAt.value ? zoneTime(today.updatedAt.value, property.clock?.timezone) : ''))

const label = (s: ComponentStatus): string => t(`dashboard.page.${s}` as never)
const variant = (s: ComponentStatus) => (s === 'up' ? ('success' as const) : s === 'down' ? ('destructive' as const) : ('outline' as const))
</script>

<template>
  <PageHeader :title="t('dashboard.page.title')" :description="description || undefined">
    <template v-if="property.current" #marks>
      <Badge variant="secondary" data-testid="property-name">{{ property.current.name }}</Badge>
    </template>
    <template #actions>
      <span v-if="updated" class="text-sm text-muted-foreground" data-testid="updated">{{ t('today.refreshed', { time: updated }) }}</span>
      <Button variant="outline" size="sm" :disabled="today.refreshing.value" data-testid="refresh" @click="today.reload()">
        <RefreshCw :class="today.refreshing.value && 'animate-spin'" />
        {{ today.refreshing.value ? t('common.loading') : t('common.refresh') }}
      </Button>
      <Button v-if="can('frontdesk.checkin')" as-child size="sm" variant="outline" data-testid="quick-walk-in">
        <RouterLink to="/walk-in"><DoorOpen />{{ t('dashboard.page.walkIn') }}</RouterLink>
      </Button>
      <Button v-if="can('reservation.create')" as-child size="sm" data-testid="quick-new-reservation">
        <RouterLink to="/reservations/new"><CalendarPlus />{{ t('dashboard.page.newReservation') }}</RouterLink>
      </Button>
    </template>
  </PageHeader>

  <template v-if="property.current">
    <div v-if="today.canFront.value || today.canBoard.value" class="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-[repeat(auto-fill,minmax(13rem,1fr))]" data-testid="strip">
      <KpiCard v-if="today.canFront.value" data-testid="strip-occupancy" :label="t('today.occupancy')" :value="tonight ? formatPercent(tonight.occupancy_percent) : '–'" :icon="Percent">
        <template v-if="tonight">
          <span class="block">{{ t('today.occupancyHint', { held: tonight.held, sellable: tonight.sellable }) }}</span>
          <span class="mt-1.5 block h-1.5 overflow-hidden rounded-full bg-muted" role="presentation"><span class="block h-full rounded-full bg-primary" data-testid="strip-meter" :style="{ width: `${Math.min(100, Math.max(0, Number(tonight.occupancy_percent)))}%` }" /></span>
        </template>
      </KpiCard>
      <KpiCard v-if="today.canFront.value" data-testid="strip-arrivals" :label="t('today.arrivals')" :value="today.arrivals.value.loaded ? `${arrivalsLeft.length} / ${arrivalsTotal}` : '–'" :hint="today.arrivals.value.loaded ? t('today.arrivalsHint', { n: today.arrivedCount.value }) : ''" :icon="DoorOpen" />
      <KpiCard v-if="today.canFront.value" data-testid="strip-departures" :label="t('today.departures')" :value="today.departures.value.loaded ? t('today.departuresValue', { n: departuresLeft.length }) : '–'" :hint="t('today.departuresHint')" :icon="DoorClosed" />
      <KpiCard v-if="today.canBoard.value" data-testid="strip-ready" :label="t('today.readyRooms')" :value="today.rooms.value.loaded ? counts.ready : '–'" :hint="today.rooms.value.loaded ? t('today.readyHint', { dirty: counts.dirty, cleaning: counts.cleaning }) : ''" :icon="BedDouble" />
    </div>

    <div class="mb-4 grid gap-4 lg:grid-cols-3" data-testid="columns">
      <TodayCard v-if="today.canFront.value" test-id="col-arrivals" :title="t('today.arrivalsTitle')" :count="today.arrivals.value.loaded ? t('today.left', { n: arrivalsLeft.length }) : ''" to="/arrivals" :loaded="today.arrivals.value.loaded" :failed="today.arrivals.value.failed">
        <p v-if="!arrivalRows.length" class="m-0 text-sm text-muted-foreground" data-testid="col-arrivals-empty">{{ t('today.noArrivals') }}</p>
        <ul v-else class="m-0 list-none divide-y divide-border p-0">
          <li v-for="a in arrivalRows" :key="a.reservation_room_id" class="flex flex-wrap items-center gap-x-3 gap-y-1 py-2.5" :data-testid="`arrival-${a.reservation_room_id}`">
            <div class="min-w-0 flex-1">
              <div class="truncate font-medium">{{ a.guest_name || a.confirmation_number }}</div>
              <div class="text-xs text-muted-foreground">{{ a.room_number || t('frontDesk.arrivals.noRoom') }} · {{ a.room_type_code }}</div>
            </div>
            <StatusBadge v-if="a.room_id && a.housekeeping_status" domain="housekeeping" :status="a.housekeeping_status" :data-testid="`arrival-chip-${a.reservation_room_id}`" />
            <Badge v-else variant="outline" :data-testid="`arrival-chip-${a.reservation_room_id}`">{{ t('today.noRoomYet') }}</Badge>
            <Button v-if="canCheckIn(a)" size="sm" :data-testid="`check-in-${a.reservation_room_id}`" @click="checkIn = a">{{ t('frontDesk.arrivals.action.checkIn') }}</Button>
          </li>
        </ul>
      </TodayCard>

      <TodayCard v-if="today.canFront.value" test-id="col-departures" :title="t('today.departuresTitle')" :count="today.departures.value.loaded ? t('today.left', { n: departuresLeft.length }) : ''" to="/departures" :loaded="today.departures.value.loaded" :failed="today.departures.value.failed">
        <p v-if="!departureRows.length" class="m-0 text-sm text-muted-foreground" data-testid="col-departures-empty">{{ t('today.noDepartures') }}</p>
        <ul v-else class="m-0 list-none divide-y divide-border p-0">
          <li v-for="d in departureRows" :key="d.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 py-2.5" :data-testid="`departure-${d.id}`">
            <div class="min-w-0 flex-1">
              <div class="truncate font-medium">{{ d.guest.name }}</div>
              <div class="text-xs text-muted-foreground">{{ d.room.number }} · {{ d.room.room_type_code }}</div>
            </div>
            <div class="text-right text-sm tabular-nums" :data-testid="`departure-balance-${d.id}`">
              {{ balanceText(d) }}
              <span v-if="d.balance.status !== 'NO_FOLIO'" class="block text-xs text-muted-foreground">{{ t('today.balance') }}</span>
            </div>
            <Button v-if="can('frontdesk.checkout')" size="sm" :disabled="checkOut?.loading" :data-testid="`check-out-${d.id}`" @click="openCheckOut(d)">{{ t('frontDesk.inHouse.action.checkOut') }}</Button>
          </li>
        </ul>
      </TodayCard>

      <TodayCard test-id="col-attention" :title="t('today.attentionTitle')" :count="attention.length ? String(attention.length) : ''" :loaded="attentionLoaded" :failed="attentionFailed">
        <ul class="m-0 list-none divide-y divide-border p-0">
          <li v-for="x in attention" :key="x.id" class="flex items-start gap-2.5 py-2.5" :data-testid="`attention-${x.id}`">
            <component :is="x.id === 'audit' ? MoonStar : AlertTriangle" :class="['mt-0.5 size-4 shrink-0', x.tone === 'warn' ? 'text-warning-text' : 'text-muted-foreground']" aria-hidden="true" />
            <div class="min-w-0">
              <RouterLink v-if="x.to" :to="x.to" class="font-medium text-foreground no-underline hover:underline">{{ x.title }}</RouterLink>
              <div v-else class="font-medium">{{ x.title }}</div>
              <div class="text-xs text-muted-foreground">{{ x.detail }}</div>
            </div>
          </li>
        </ul>
      </TodayCard>
    </div>

    <Card v-if="today.canFront.value" class="mb-4" data-testid="chart">
      <CardHeader>
        <CardTitle>{{ t('today.chartTitle', { n: today.nights.value.data.length || 14 }) }} <small class="font-normal text-muted-foreground">· {{ t('today.chartHint') }}</small></CardTitle>
      </CardHeader>
      <CardContent>
        <p v-if="today.nights.value.failed" class="m-0 mb-3 text-sm text-warning-text" role="status" data-testid="chart-failed">{{ t('today.couldNotLoad') }}</p>
        <NightsChart v-if="bars.length" :bars="bars" test-prefix="night" />
        <p v-else-if="!today.nights.value.failed" class="m-0 text-sm text-muted-foreground">{{ t('today.loading') }}</p>
      </CardContent>
    </Card>
  </template>

  <div v-if="(property.loaded && !property.hasProperties) || auth.isAdmin" class="grid gap-4 md:grid-cols-2">
    <Card v-if="property.loaded && !property.hasProperties">
      <CardHeader><CardTitle>{{ t('dashboard.page.setupTitle') }}</CardTitle></CardHeader>
      <CardContent>
        <p class="m-0 mb-3 text-sm text-muted-foreground">{{ t('dashboard.page.setupBody') }}</p>
        <RouterLink to="/setup/properties/new">{{ t('dashboard.page.createProperty') }}</RouterLink>
      </CardContent>
    </Card>

    <Card v-if="auth.isAdmin" aria-labelledby="status-title" data-testid="system-status-card">
      <CardHeader class="flex-row items-center justify-between">
        <CardTitle id="status-title">{{ t('dashboard.page.systemStatus') }}</CardTitle>
        <Button variant="outline" size="sm" :disabled="system.checking" @click="system.check()">
          {{ system.checking ? t('dashboard.page.checking') : t('common.refresh') }}
        </Button>
      </CardHeader>
      <CardContent>
        <dl class="m-0 divide-y divide-border text-sm">
          <div class="flex items-center justify-between py-2">
            <dt class="font-medium">{{ t('dashboard.page.api') }}</dt>
            <dd class="m-0"><Badge :variant="variant(system.apiStatus)" data-testid="api-status">{{ label(system.apiStatus) }}</Badge></dd>
          </div>
          <div class="flex items-center justify-between py-2">
            <dt class="font-medium">{{ t('dashboard.page.database') }}</dt>
            <dd class="m-0"><Badge :variant="variant(system.databaseStatus)" data-testid="db-status">{{ label(system.databaseStatus) }}</Badge></dd>
          </div>
        </dl>
        <p v-if="system.lastError" class="m-0 mt-3 text-sm text-destructive" role="alert">
          {{ system.lastError.message }}
          <code>{{ system.lastError.code }}</code>
          <span v-if="system.lastError.requestId"> · {{ t('dashboard.page.request', { id: system.lastError.requestId }) }}</span>
        </p>
        <p v-if="system.checkedAt" class="m-0 mt-3 text-sm text-muted-foreground">{{ t('dashboard.page.lastChecked', { time: system.checkedAt.toLocaleTimeString() }) }}</p>
      </CardContent>
    </Card>
  </div>

  <Sheet v-model:open="checkInOpen">
    <SheetContent v-if="checkIn" class="p-6" data-testid="checkin-sheet">
      <SheetTitle class="text-lg">{{ t('frontDesk.checkIn.title', { guest: checkIn.guest_name || checkIn.confirmation_number, type: checkIn.room_type_code }) }}</SheetTitle>
      <SheetDescription class="mt-1">
        {{ t('frontDesk.checkIn.subtitle', { confirmation: checkIn.confirmation_number, arrival: formatDate(checkIn.arrival_date), departure: formatDate(checkIn.departure_date) }) }}
      </SheetDescription>
      <CheckInPanel :key="checkIn.reservation_room_id" :arrival="checkIn" class="mt-4" @done="checkedIn" @cancel="checkIn = null" />
    </SheetContent>
  </Sheet>

  <Sheet v-model:open="checkOutOpen">
    <SheetContent v-if="checkOut?.detail" class="p-6" data-testid="checkout-sheet">
      <SheetTitle class="text-lg">{{ t('today.checkOutTitle', { guest: checkOut.row.guest.name }) }}</SheetTitle>
      <SheetDescription class="mt-1">{{ t('today.checkOutSubtitle', { stay: checkOut.row.stay_number, room: checkOut.row.room.number }) }}</SheetDescription>
      <CheckOutWizard :detail="checkOut.detail" class="mt-4" @done="checkedOut" @cancel="checkOut = null" />
    </SheetContent>
  </Sheet>
</template>
