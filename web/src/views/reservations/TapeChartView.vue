<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { ChevronLeft, ChevronRight } from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { TapeBooking, TapeChart, TapeRoom } from '@/api/types'
import PageHeader from '@/components/app/PageHeader.vue'
import RoomLink from '@/components/app/RoomLink.vue'
import { statusLegend, statusSwatch } from '@/components/app/statusMap'
import TapeDatesDialog from '@/components/tape/TapeDatesDialog.vue'
import TapeMoveDialog from '@/components/tape/TapeMoveDialog.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { toast } from '@/composables/useToast'
import { i18n, t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { windowDates } from '@/utils/blocks'
import { addDays } from '@/utils/dates'
import { canChangeArrival, canChangeDates, canMove, columnsOf, datesVerdict, dropVerdict, isOpenStay, nightsOf, roomOf, type TapeRights, type TapeVerdict } from '@/utils/tape'

/**
 * The tape chart: one element per booking, placed on a grid (a column per night). Clicking a block opens its menu (move the room, change the dates, open the reservation), which is the way for the
 * keyboard and the phone. With a mouse a block can also be dragged to another room (a move) and its ends pulled (the dates); both open the same dialog the menu does, which always asks for
 * confirmation. What the chart shows as free or taken is a hint made from the data it holds; the server decides when the change is confirmed.
 */
const auth = useAuthStore()
const property = usePropertyStore()

const days = ref(14)
const start = ref('')
const chart = ref<TapeChart | null>(null)
const error = ref<ApiError | null>(null)

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')
const dates = computed(() => (start.value ? windowDates(start.value, days.value) : []))
const rights = computed<TapeRights>(() => ({
  update: auth.can('reservation.update', property.currentId),
  upgrade: auth.can('reservation.upgrade', property.currentId),
  move: auth.can('frontdesk.room_move', property.currentId),
}))
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

// ---- the grid
const LABEL = '7rem'
const gridStyle = computed(() => ({ gridTemplateColumns: `${LABEL} repeat(${dates.value.length}, minmax(2.75rem, 1fr))`, '--days': String(dates.value.length) }))

interface Placed { booking: TapeBooking; key: string; start: number; span: number; clippedLeft: boolean; clippedRight: boolean; arrival: string; departure: string }
const barKey = (b: TapeBooking) => `${b.reservation_room_id}-${b.arrival_date}`

/** Where a booking sits on the window; while it is pulled, with the dates it is pulled to. */
function place(b: TapeBooking): Placed | null {
  const d = drag.value
  const live = d && d.active && d.kind !== 'move' && d.booking === b
  const arrival = live ? d.arrival : b.arrival_date
  const departure = live ? d.departure : b.departure_date
  const c = columnsOf(arrival, departure, start.value, days.value)
  return c ? { booking: b, key: barKey(b), arrival, departure, ...c } : null
}
const placed = (bookings: TapeBooking[]): Placed[] => bookings.map(place).filter((p): p is Placed => p !== null)

const blockPlaced = (room: TapeRoom) => room.blocks.map((b) => ({ block: b, c: columnsOf(b.start_date, b.end_date, start.value, days.value) })).filter((x) => x.c !== null)

const barClass = (b: TapeBooking) => cn('flex h-8 min-w-0 items-center gap-1 overflow-hidden whitespace-nowrap border-y px-1.5 text-left text-[13px] leading-none no-underline', statusSwatch('reservation', b.status).fill, b.status === 'CONFIRMED' && 'border-status-inhouse')

// ---- the menu of a block, and the dialogs it and the drag lead to
const menu = ref<string | null>(null)
const moveDialog = ref<{ open: boolean; booking: TapeBooking | null; from: TapeRoom | null; target: number | null }>({ open: false, booking: null, from: null, target: null })
const datesDialog = ref<{ open: boolean; booking: TapeBooking | null; room: TapeRoom | null; arrival: string; departure: string }>({ open: false, booking: null, room: null, arrival: '', departure: '' })

function openMove(b: TapeBooking, from: TapeRoom | null, target: number | null = null): void {
  menu.value = null
  moveDialog.value = { open: true, booking: b, from, target }
}
function openDates(b: TapeBooking, room: TapeRoom | null, arrival = b.arrival_date, departure = b.departure_date): void {
  menu.value = null
  datesDialog.value = { open: true, booking: b, room, arrival, departure }
}
const menuItems = (b: TapeBooking) => ({ move: canMove(b, rights.value), dates: canChangeDates(b, rights.value) })
function done(message: string): void {
  toast.success(message)
}

// ---- drag (a mouse; never the only way)
const fine = ref(false)
let mql: MediaQueryList | null = null
const syncFine = () => { fine.value = !!mql?.matches }
onMounted(() => {
  mql = typeof window.matchMedia === 'function' ? window.matchMedia('(pointer: fine)') : null
  syncFine()
  mql?.addEventListener?.('change', syncFine)
})
onBeforeUnmount(() => {
  mql?.removeEventListener?.('change', syncFine)
  stopDrag()
})

type DragKind = 'move' | 'left' | 'right'
interface Drag {
  kind: DragKind
  booking: TapeBooking
  from: TapeRoom | null
  x: number
  y: number
  active: boolean
  hoverRoom: number | null
  arrival: string
  departure: string
  verdicts: Map<number, TapeVerdict>
}
const drag = ref<Drag | null>(null)
let suppressClick = false
const headCell = ref<HTMLElement | null>(null)

const roomVerdicts = (b: TapeBooking, from: TapeRoom | null): Map<number, TapeVerdict> =>
  new Map((chart.value?.rooms ?? []).map((r) => [r.room_id, dropVerdict(b, r, from?.room_id ?? null, businessDate.value, rights.value)]))

function rowAt(x: number, y: number): number | null {
  const els = typeof document.elementsFromPoint === 'function' ? document.elementsFromPoint(x, y) : [document.elementFromPoint?.(x, y)]
  for (const el of els) {
    const row = el instanceof Element ? el.closest<HTMLElement>('[data-room-id]') : null
    if (row) return Number(row.dataset.roomId)
  }
  return null
}

function startDrag(e: PointerEvent, b: TapeBooking, from: TapeRoom | null, kind: DragKind): void {
  if (!fine.value || e.button !== 0) return
  const allowed = kind === 'move' ? canMove(b, rights.value) : kind === 'right' ? canChangeDates(b, rights.value) : canChangeArrival(b, rights.value)
  if (!allowed) return
  if (kind !== 'move') e.stopPropagation() // the handle is not the block
  drag.value = { kind, booking: b, from, x: e.clientX, y: e.clientY, active: false, hoverRoom: null, arrival: b.arrival_date, departure: b.departure_date, verdicts: kind === 'move' ? roomVerdicts(b, from) : new Map() }
  window.addEventListener('pointermove', onMove)
  window.addEventListener('pointerup', onUp)
  window.addEventListener('pointercancel', stopDrag)
  window.addEventListener('keydown', onKey)
}

function onMove(e: PointerEvent): void {
  const d = drag.value
  if (!d) return
  if (!d.active) {
    if (Math.hypot(e.clientX - d.x, e.clientY - d.y) < 4) return
    d.active = true
    menu.value = null
  }
  if (d.kind === 'move') {
    d.hoverRoom = rowAt(e.clientX, e.clientY)
    return
  }
  const width = headCell.value?.getBoundingClientRect().width || 1
  const delta = Math.round((e.clientX - d.x) / width)
  const b = d.booking
  if (d.kind === 'right') {
    const min = isOpenStay(b) ? addDays(businessDate.value, 1) : addDays(b.arrival_date, 1)
    const next = addDays(b.departure_date, delta)
    d.departure = next < min ? min : next
  } else {
    const next = addDays(b.arrival_date, delta)
    const max = addDays(b.departure_date, -1)
    d.arrival = next > max ? max : next
  }
}

function resizeVerdict(d: Drag) {
  return datesVerdict(d.booking, d.from ?? roomOf(chart.value!, d.booking), d.arrival, d.departure, businessDate.value, rights.value)
}

function onUp(): void {
  const d = drag.value
  stopDrag()
  if (!d || !d.active) return // a click: the menu of the block opens
  suppressClick = true
  window.setTimeout(() => (suppressClick = false), 0)
  if (d.kind === 'move') {
    const target = d.hoverRoom !== null ? d.verdicts.get(d.hoverRoom) : undefined
    if (d.hoverRoom !== null && target?.ok) openMove(d.booking, d.from, d.hoverRoom)
    return
  }
  const changed = d.arrival !== d.booking.arrival_date || d.departure !== d.booking.departure_date
  if (changed && resizeVerdict(d).ok) openDates(d.booking, d.from, d.arrival, d.departure)
}

function onKey(e: KeyboardEvent): void {
  if (e.key === 'Escape') stopDrag()
}

function stopDrag(): void {
  drag.value = null
  window.removeEventListener('pointermove', onMove)
  window.removeEventListener('pointerup', onUp)
  window.removeEventListener('pointercancel', stopDrag)
  window.removeEventListener('keydown', onKey)
}

/** A click that ends a drag is not a click on the block. */
function guardClick(e: Event): void {
  if (suppressClick) {
    e.preventDefault()
    e.stopPropagation()
  }
}

const dragging = computed(() => !!drag.value?.active)
/** The columns of the dragged block: the nights a drop would cover. */
const dropZone = computed(() => {
  const d = drag.value
  if (!d?.active || d.kind !== 'move') return null
  return columnsOf(d.booking.arrival_date, d.booking.departure_date, start.value, days.value)
})
const zoneClass = (state: 'ok' | 'no' | 'hover' | 'none'): string => ({
  no: 'status-hatch bg-muted/70 opacity-80',
  ok: 'ring-1 ring-inset ring-primary/40',
  hover: 'bg-primary/10 ring-2 ring-inset ring-primary',
  none: '',
})[state]
const rowState = (roomId: number): 'ok' | 'no' | 'hover' | 'none' => {
  const d = drag.value
  if (!d?.active || d.kind !== 'move') return 'none'
  const v = d.verdicts.get(roomId)
  if (!v || (!v.ok && v.reason === 'same')) return 'none'
  if (!v.ok) return 'no'
  return d.hoverRoom === roomId ? 'hover' : 'ok'
}
const refusalText = (roomId: number): string => {
  const v = drag.value?.verdicts.get(roomId)
  return v && !v.ok ? t(`tapeChart.refusal.${v.reason}`) : ''
}
const pullRefusal = computed(() => {
  const d = drag.value
  if (!d?.active || d.kind === 'move') return ''
  const v = resizeVerdict(d)
  return v.ok ? '' : t(`tapeChart.dates.refusal.${v.reason}`)
})

watch(() => property.currentId, () => {
  start.value = ''
  chart.value = null
  void load()
}, { immediate: true })
watch(businessDate, () => {
  if (!chart.value) void load()
})

const unassignedRows = computed(() => (chart.value?.unassigned ?? []).map((u) => ({ ...u, rows: placed(u.bookings) })))
const ghost = computed(() => {
  const d = drag.value
  if (!d?.active || d.kind !== 'move') return null
  return place(d.booking)
})
</script>

<template>
  <PageHeader :title="t('tapeChart.title')">
    <template #actions>
      <Button variant="outline" size="sm" @click="shift(-7)"><ChevronLeft />{{ t('tapeChart.week') }}</Button>
      <Button variant="outline" size="sm" @click="start = businessDate; load()">{{ t('tapeChart.businessDate') }}</Button>
      <Button variant="outline" size="sm" @click="shift(7)">{{ t('tapeChart.week') }}<ChevronRight /></Button>
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
    <ul class="m-0 mb-3 flex list-none flex-wrap gap-4 p-0 text-[13px] text-muted-foreground" data-testid="legend">
      <li v-for="l in legend" :key="l.status" class="flex items-center gap-1.5" :data-legend="l.status"><span :class="cn('h-3 w-5 rounded-sm border', l.swatch)" aria-hidden="true" />{{ l.label }}</li>
      <li class="flex items-center gap-1.5"><span class="h-3 w-5 rounded-sm bg-primary/10 ring-1 ring-primary/40" aria-hidden="true" />{{ t('tapeChart.legendToday') }}</li>
    </ul>
    <p v-if="rights.update || rights.move" class="m-0 mb-3 text-[13px] text-muted-foreground" data-testid="tape-hint">{{ fine ? t('tapeChart.hintDrag') : t('tapeChart.hintMenu') }}</p>
    <p v-if="pullRefusal" class="alert warning m-0 mb-3" role="status" data-testid="tape-pull-refusal">{{ pullRefusal }}</p>

    <Card class="overflow-x-auto">
      <div class="relative min-w-[720px] text-[13px]" data-testid="tape" :class="dragging && 'select-none'">
        <!-- the dates -->
        <div class="sticky top-0 z-20 grid bg-card" :style="gridStyle" role="row">
          <div class="sticky left-0 z-30 border-b border-border bg-card" />
          <div
            v-for="(d, i) in dates" :key="d" :ref="(el) => { if (i === 0) headCell = el as HTMLElement | null }" :data-day-head="d"
            :class="cn('border-b border-l border-border px-1 py-1.5 text-center font-medium text-muted-foreground', isWeekend(d) && 'bg-muted/60', d === businessDate && 'bg-primary/10 text-primary')"
            role="columnheader"
          >
            <span class="block text-[11px] uppercase">{{ weekday(d) }}</span>{{ dayLabel(d) }}
          </div>
        </div>

        <template v-for="(room, i) in chart.rooms" :key="room.room_id">
          <div v-if="i === 0 || chart.rooms[i - 1]?.room_type_id !== room.room_type_id" class="border-b border-t border-border bg-muted px-2 py-1 text-left text-[13px] font-semibold">{{ room.room_type_code }}</div>
          <div
            class="relative grid h-10 border-b border-border"
            :style="gridStyle" :data-room-id="room.room_id" :data-testid="`room-${room.room_number}`" :data-drop="rowState(room.room_id)" :title="rowState(room.room_id) === 'no' ? refusalText(room.room_id) : undefined" role="row"
          >
            <div class="sticky left-0 z-10 flex items-center border-r border-border bg-card px-2 font-medium" role="rowheader"><RoomLink :room-id="room.room_id" :number="room.room_number" /></div>
            <!-- the nights: the lines between them, and the weekend and the business date -->
            <div class="pointer-events-none z-0 grid" :style="{ gridColumn: '2 / -1', gridRow: '1', gridTemplateColumns: `repeat(${dates.length}, minmax(0, 1fr))` }" aria-hidden="true">
              <span v-for="d in dates" :key="d" :class="cn('border-l border-border', isWeekend(d) && 'bg-muted/40', d === businessDate && 'bg-primary/10')" />
            </div>

            <!-- while a block is dragged: only the nights of that block are marked in this room, not the whole row -->
            <span
              v-if="dropZone && rowState(room.room_id) !== 'none'" :class="cn('pointer-events-none z-[5] my-0.5 rounded-sm', zoneClass(rowState(room.room_id)))"
              :style="{ gridColumn: `${dropZone.start + 2} / span ${dropZone.span}`, gridRow: '1' }" data-testid="tape-drop-zone" :data-zone="rowState(room.room_id)" aria-hidden="true"
            />

            <template v-for="p in placed(room.bookings)" :key="p.key">
              <Popover :open="menu === p.key" @update:open="(v: boolean) => (menu = v ? p.key : null)">
                <PopoverTrigger as-child>
                  <button
                    type="button" :class="cn(barClass(p.booking), 'relative z-10 my-1 touch-none', fine && (canMove(p.booking, rights)) && 'cursor-grab', drag?.active && drag.booking === p.booking && drag.kind === 'move' && 'opacity-40', pullRefusal && drag?.booking === p.booking && 'ring-2 ring-destructive')"
                    :style="{ gridColumn: `${p.start + 2} / span ${p.span}`, gridRow: '1' }" :data-testid="`bar-${room.room_number}-${p.start >= 0 ? dates[p.start] : ''}`" :data-booking="p.booking.reservation_room_id"
                    :aria-label="t('tapeChart.barLabel', { guest: p.booking.guest_name || p.booking.confirmation_number, number: p.booking.confirmation_number, from: p.arrival, to: p.departure })" aria-haspopup="menu"
                    :title="`${p.booking.confirmation_number} ${p.booking.guest_name ?? ''}`"
                    @pointerdown="startDrag($event, p.booking, room, 'move')" @click.capture="guardClick"
                  >
                    <span v-if="p.clippedLeft" aria-hidden="true">‹</span>
                    <span
                      v-if="fine && !p.clippedLeft && canChangeArrival(p.booking, rights)" class="absolute inset-y-0 left-0 z-20 w-2 cursor-ew-resize" :data-handle="`left-${p.booking.reservation_room_id}`" aria-hidden="true"
                      @pointerdown="startDrag($event, p.booking, room, 'left')"
                    />
                    <span class="min-w-0 flex-1 truncate font-medium">{{ p.booking.guest_name || p.booking.confirmation_number }}<span class="font-normal opacity-80"> · {{ t('tapeChart.nightsN', { n: nightsOf(p.arrival, p.departure) }, nightsOf(p.arrival, p.departure)) }}</span></span>
                    <span v-if="p.clippedRight" aria-hidden="true">›</span>
                    <span
                      v-if="fine && !p.clippedRight && canChangeDates(p.booking, rights) && (p.booking.status === 'CONFIRMED' || p.booking.open_segment)" class="absolute inset-y-0 right-0 z-20 w-2 cursor-ew-resize" :data-handle="`right-${p.booking.reservation_room_id}`" aria-hidden="true"
                      @pointerdown="startDrag($event, p.booking, room, 'right')"
                    />
                  </button>
                </PopoverTrigger>
                <PopoverContent class="w-52 p-1">
                  <div role="menu" :aria-label="t('tapeChart.menuLabel')" data-testid="tape-menu">
                    <Button v-if="menuItems(p.booking).move" type="button" variant="ghost" size="sm" class="w-full justify-start" role="menuitem" data-testid="menu-move" @click="openMove(p.booking, room)">{{ t('tapeChart.menu.move') }}</Button>
                    <Button v-if="menuItems(p.booking).dates" type="button" variant="ghost" size="sm" class="w-full justify-start" role="menuitem" data-testid="menu-dates" @click="openDates(p.booking, room)">{{ t('tapeChart.menu.dates') }}</Button>
                    <Button as-child variant="ghost" size="sm" class="w-full justify-start" role="menuitem" data-testid="menu-open"><RouterLink :to="`/reservations/${p.booking.reservation_id}`" @click="menu = null">{{ t('tapeChart.menu.open') }}</RouterLink></Button>
                  </div>
                </PopoverContent>
              </Popover>
            </template>

            <template v-for="x in blockPlaced(room)" :key="x.block.id">
              <span
                :class="cn('relative z-10 my-1 flex h-8 items-center overflow-hidden border-y px-1.5 text-[13px]', statusSwatch('block', x.block.block_type).fill)"
                :style="{ gridColumn: `${x.c!.start + 2} / span ${x.c!.span}`, gridRow: '1' }" :data-testid="`block-${room.room_number}-${dates[x.c!.start]}`"
              >{{ x.block.block_type }}</span>
            </template>

            <!-- while a block is dragged onto this row: where it would sit -->
            <span
              v-if="ghost && rowState(room.room_id) === 'hover'" class="pointer-events-none relative z-10 my-1 flex h-8 items-center rounded-sm border-2 border-primary bg-primary/20 px-1.5 text-[13px] font-medium"
              :style="{ gridColumn: `${ghost.start + 2} / span ${ghost.span}`, gridRow: '1' }" data-testid="tape-ghost"
            >{{ ghost.booking.guest_name || ghost.booking.confirmation_number }}</span>
          </div>
        </template>

        <!-- bookings that have no room yet: one bar each, and a block dragged from here onto a room assigns it -->
        <template v-for="u in unassignedRows" :key="u.room_type_id">
          <div class="border-b border-t border-border bg-muted px-2 py-1 text-left text-[13px] font-semibold" :data-testid="`unassigned-${u.room_type_code}`">{{ t('tapeChart.notAssigned') }} · {{ u.room_type_code }} <span class="font-normal text-muted-foreground">({{ t('tapeChart.bookings', { n: u.bookings.length }) }})</span></div>
          <div v-for="p in u.rows" :key="p.key" class="relative grid h-10 border-b border-border" :style="gridStyle" :data-testid="`unassigned-row-${p.booking.reservation_room_id}`" role="row">
            <div class="sticky left-0 z-10 flex items-center border-r border-border bg-card px-2 text-[13px] text-muted-foreground" role="rowheader">{{ p.booking.confirmation_number }}</div>
            <div class="pointer-events-none z-0 grid" :style="{ gridColumn: '2 / -1', gridRow: '1', gridTemplateColumns: `repeat(${dates.length}, minmax(0, 1fr))` }" aria-hidden="true">
              <span v-for="d in dates" :key="d" :class="cn('border-l border-border', isWeekend(d) && 'bg-muted/40', d === businessDate && 'bg-primary/10')" />
            </div>
            <Popover :open="menu === p.key" @update:open="(v: boolean) => (menu = v ? p.key : null)">
              <PopoverTrigger as-child>
                <button
                  type="button" :class="cn(barClass(p.booking), 'relative z-10 my-1 touch-none', fine && canMove(p.booking, rights) && 'cursor-grab', drag?.active && drag.booking === p.booking && 'opacity-40')"
                  :style="{ gridColumn: `${p.start + 2} / span ${p.span}`, gridRow: '1' }" :data-testid="`bar-unassigned-${p.booking.reservation_room_id}`" :data-booking="p.booking.reservation_room_id" aria-haspopup="menu"
                  :aria-label="t('tapeChart.barLabel', { guest: p.booking.guest_name || p.booking.confirmation_number, number: p.booking.confirmation_number, from: p.arrival, to: p.departure })"
                  @pointerdown="startDrag($event, p.booking, null, 'move')" @click.capture="guardClick"
                >
                  <span class="min-w-0 flex-1 truncate font-medium">{{ p.booking.guest_name || p.booking.confirmation_number }}<span class="font-normal opacity-80"> · {{ t('tapeChart.nightsN', { n: nightsOf(p.arrival, p.departure) }, nightsOf(p.arrival, p.departure)) }}</span></span>
                </button>
              </PopoverTrigger>
              <PopoverContent class="w-52 p-1">
                <div role="menu" :aria-label="t('tapeChart.menuLabel')" data-testid="tape-menu">
                  <Button v-if="menuItems(p.booking).move" type="button" variant="ghost" size="sm" class="w-full justify-start" role="menuitem" data-testid="menu-move" @click="openMove(p.booking, null)">{{ t('tapeChart.menu.assign') }}</Button>
                  <Button v-if="menuItems(p.booking).dates" type="button" variant="ghost" size="sm" class="w-full justify-start" role="menuitem" data-testid="menu-dates" @click="openDates(p.booking, null)">{{ t('tapeChart.menu.dates') }}</Button>
                  <Button as-child variant="ghost" size="sm" class="w-full justify-start" role="menuitem" data-testid="menu-open"><RouterLink :to="`/reservations/${p.booking.reservation_id}`" @click="menu = null">{{ t('tapeChart.menu.open') }}</RouterLink></Button>
                </div>
              </PopoverContent>
            </Popover>
          </div>
        </template>
      </div>
    </Card>

    <TapeMoveDialog
      v-model:open="moveDialog.open" :booking="moveDialog.booking" :from-room="moveDialog.from" :rooms="chart.rooms" :target="moveDialog.target" :rights="rights"
      @done="done" @changed="load"
    />
    <TapeDatesDialog
      v-model:open="datesDialog.open" :booking="datesDialog.booking" :room="datesDialog.room" :arrival="datesDialog.arrival" :departure="datesDialog.departure" :rights="rights"
      @done="done" @changed="load"
    />
  </template>
</template>
