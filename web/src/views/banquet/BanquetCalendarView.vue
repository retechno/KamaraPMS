<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetCalendar, BanquetCalendarLock, BanquetCalendarSession, BanquetEvent, BanquetEventType, BanquetNotice } from '@/api/types'
import BanquetEventDialog from '@/components/banquet/BanquetEventDialog.vue'
import BanquetSessionDialog from '@/components/banquet/BanquetSessionDialog.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { DateInput } from '@/components/ui/date-input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { geometry, heldSpan, hhmm, layoutLanes, lockSpan, locksOf, minutesOf, sessionsOf, TONE_CLASS, toneOf, weekDays } from '@/utils/banquet'
import { addDays } from '@/utils/dates'
import { formatDateWeekday } from '@/utils/format'

/**
 * The schedule of the meeting rooms (docs/architecture/36-banquet.md 6.1, 6.2). The day is one column per room against the hours, the week one row per room against the days. A block is drawn from the
 * start of its set-up to the end of its tear-down; a confirmed session is solid, the first option amber, an option behind another grey with its place; sessions that overlap in a room sit side by side.
 * A room that another booking locks (the parts of a combined room, the combined rooms of a part) shows the hold hatched with the room it is booked in: the server says which, from the holds.
 * A click on an empty slot opens a new booking.
 * On a phone the day is a list in time order: a grid of rooms does not fit in 390 pixels.
 */
const property = usePropertyStore()
const auth = useAuthStore()
const router = useRouter()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('banquet.manage', pid.value))

const view = ref<'day' | 'week'>('day')
const date = ref('')
const calendar = ref<BanquetCalendar | null>(null)
const types = ref<BanquetEventType[]>([])
const notices = ref<BanquetNotice[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)

const HOUR_PX = 40
const range = { from: 6 * 60, to: 24 * 60 }
const hours = computed(() => Array.from({ length: (range.to - range.from) / 60 }, (_, i) => range.from / 60 + i))
const gridHeight = computed(() => hours.value.length * HOUR_PX)
const days = computed(() => (view.value === 'week' ? weekDays(date.value) : [date.value]))
const rooms = computed(() => (calendar.value?.rooms ?? []).filter((r) => r.is_active))
const sessions = computed(() => calendar.value?.sessions ?? [])
const locks = computed(() => calendar.value?.locks ?? [])
interface AgendaItem {
  key: string
  minutes: number
  session?: BanquetCalendarSession
  lock?: BanquetCalendarLock
}
/** The day as a list: the sessions and the locks of the rooms in time order. */
const dayAgenda = computed<AgendaItem[]>(() => {
  const list: AgendaItem[] = sessions.value.filter((s) => s.session_date === date.value).map((s) => ({ key: `s${s.session_id}`, minutes: minutesOf(s.start_time), session: s }))
  for (const l of locks.value) {
    const span = lockSpan(l, date.value)
    if (span && rooms.value.some((r) => r.id === l.room_id)) list.push({ key: `l${l.session_id}-${l.room_id}`, minutes: span.start, lock: l })
  }
  return list.sort((a, b) => a.minutes - b.minutes || a.key.localeCompare(b.key))
})
const roomName = (id: number): string => rooms.value.find((r) => r.id === id)?.name ?? ''

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  try {
    if (!date.value) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/today', { params: { path: { propertyId } } })
      date.value = data?.date ?? new Date().toISOString().slice(0, 10)
    }
    const from = view.value === 'week' ? days.value[0]! : date.value
    const to = view.value === 'week' ? days.value[6]! : date.value
    const [cal, ty, nt] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/banquet/calendar', { params: { path: { propertyId }, query: { from, to } } }),
      types.value.length ? Promise.resolve(null) : api.GET('/api/v1/properties/{propertyId}/banquet/event-types', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/banquet/notices', { params: { path: { propertyId }, query: { unseen: true } } }).catch(() => null),
    ])
    calendar.value = cal.data ?? null
    if (ty?.data) types.value = ty.data.data
    notices.value = nt?.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, view, date], load, { immediate: true })

function step(n: number): void {
  date.value = addDays(date.value, view.value === 'week' ? n * 7 : n)
}

async function seeNotices(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    await api.POST('/api/v1/properties/{propertyId}/banquet/notices/seen', { params: { path: { propertyId } }, body: { ids: notices.value.map((n) => n.id) } })
    notices.value = []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

// ---- a new booking from a click on an empty slot: the header first, then the first session with the slot filled in
const eventOpen = ref(false)
const sessionOpen = ref(false)
const created = ref<BanquetEvent | null>(null)
const prefill = ref<{ date?: string; room_id?: number; start?: string } | null>(null)
function bookAt(roomId: number | undefined, day: string, start?: string): void {
  if (!canManage.value) return
  prefill.value = { date: day, room_id: roomId, start }
  created.value = null
  eventOpen.value = true
}
function slotClick(e: MouseEvent, roomId: number, day: string): void {
  const el = e.currentTarget as HTMLElement
  const y = e.clientY - el.getBoundingClientRect().top
  const minutes = range.from + Math.floor((((el.clientHeight ? y / el.clientHeight : 0) * (range.to - range.from))) / 30) * 30
  bookAt(roomId, day, hhmm(minutes))
}
function onEventSaved(ev: BanquetEvent): void {
  created.value = ev
  sessionOpen.value = true
}
async function onSessionSaved(ev: BanquetEvent): Promise<void> {
  await router.push(`/banquet/events/${ev.id}`)
}

const roomLabel = (s: BanquetCalendarSession): string => `${s.event_number} ${s.event_name}`
const hours5 = (s: BanquetCalendarSession): string => `${(s.start_time ?? '').slice(0, 5)}–${(s.end_time ?? '').slice(0, 5)}`
/** The hours come first: a short block shows its first line only, and that line must say when. */
function blockLabel(s: BanquetCalendarSession): string {
  const place = toneOf(s) === 'queued' ? ` #${s.rank}` : ''
  return `${hours5(s)} · ${s.event_name} · ${s.session_name}${place}`
}
const lockText = (l: BanquetCalendarLock): string => t(l.hold_kind === 'DEFINITE' ? 'banquet.lock.definite' : 'banquet.lock.option', { room: l.via_room_name, number: l.event_number })
/** The hours of a lock on the day shown, with its set-up and tear-down. */
function lockSpanText(l: BanquetCalendarLock): string {
  const span = lockSpan(l, date.value)
  return span ? `${hhmm(span.start)}–${span.end >= 1440 ? '24:00' : hhmm(span.end)}` : ''
}
const lockTitle = (l: BanquetCalendarLock): string => `${lockText(l)} · ${l.event_name} · ${l.hold_from.slice(11)}–${l.hold_to.slice(11)}`

/** Hatching marks a room that is locked by a booking in another room. */
const HATCH = 'repeating-linear-gradient(135deg, transparent 0 5px, color-mix(in srgb, currentColor 22%, transparent) 5px 6px)'
function lockStyle(l: BanquetCalendarLock, day: string): Record<string, string> {
  const span = lockSpan(l, day)!
  const total = range.to - range.from
  const pct = (m: number): number => ((Math.min(Math.max(m, range.from), range.to) - range.from) / total) * 100
  return { top: `${pct(span.start)}%`, height: `${Math.max(0, pct(span.end) - pct(span.start))}%`, backgroundImage: HATCH }
}
const LOCK_CLASS = (l: BanquetCalendarLock): string => (l.hold_kind === 'DEFINITE' ? 'border-primary/60 text-primary' : 'border-warning/70 text-foreground')

/** Sessions of a room that overlap sit side by side, each in its lane. */
function lanesOf(roomId: number, day: string): Map<number, { lane: number; lanes: number }> {
  return layoutLanes(sessionsOf(sessions.value, roomId, day).map((s) => ({ id: s.session_id, ...heldSpan(s) })))
}
function blockStyle(s: BanquetCalendarSession, lane: { lane: number; lanes: number } | undefined): Record<string, string> {
  const g = geometry(s, range.from, range.to)
  const { lane: i, lanes } = lane ?? { lane: 0, lanes: 1 }
  return { top: `${g.top}%`, height: `${g.height}%`, left: `calc(${(i / lanes) * 100}% + 0.25rem)`, width: `calc(${100 / lanes}% - 0.5rem)` }
}
const toneKey = (s: BanquetCalendarSession): string => t(`banquet.tone.${toneOf(s)}`)
</script>

<template>
  <PageHeader :title="t('banquet.calendar.title')" :description="t('banquet.calendar.intro')">
    <template #actions>
      <Button v-if="canManage" type="button" data-testid="banquet-new" @click="bookAt(undefined, date)">{{ t('banquet.event.new') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <div v-if="notices.length" class="mb-3 flex flex-wrap items-center gap-2 rounded-md border border-border bg-muted/40 p-3 text-sm" data-testid="banquet-notices">
      <ul class="m-0 flex min-w-0 flex-1 list-none flex-col gap-0.5 p-0">
        <li v-for="n in notices" :key="n.id">
          <RouterLink :to="`/banquet/events/${n.event_id}`" class="text-primary hover:underline">{{ n.event_number }} {{ n.event_name }}</RouterLink> · {{ t(`banquet.notice.${n.kind}`) }}
        </li>
      </ul>
      <Button type="button" variant="outline" size="sm" data-testid="notices-seen" @click="seeNotices">{{ t('banquet.notice.seen') }}</Button>
    </div>

    <div class="mb-3 flex flex-wrap items-center gap-2" data-testid="calendar-bar">
      <Button type="button" variant="outline" :aria-label="t('banquet.calendar.previous')" data-testid="cal-prev" @click="step(-1)">‹</Button>
      <DateInput v-model="date" name="date" class="w-auto" :aria-label="t('banquet.calendar.date')" data-testid="cal-date" />
      <Button type="button" variant="outline" :aria-label="t('banquet.calendar.next')" data-testid="cal-next" @click="step(1)">›</Button>
      <div class="ml-auto flex gap-1" role="group" :aria-label="t('banquet.calendar.view')">
        <Button type="button" :variant="view === 'day' ? 'default' : 'outline'" :aria-pressed="view === 'day'" data-testid="view-day" @click="view = 'day'">{{ t('banquet.calendar.day') }}</Button>
        <Button type="button" :variant="view === 'week' ? 'default' : 'outline'" :aria-pressed="view === 'week'" data-testid="view-week" @click="view = 'week'">{{ t('banquet.calendar.week') }}</Button>
      </div>
    </div>
    <ul class="mb-3 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-xs" data-testid="calendar-legend">
      <li v-for="k in (['definite', 'first', 'queued', 'taken'] as const)" :key="k" class="flex items-center gap-1.5"><span class="inline-block size-3 rounded-sm border" :class="TONE_CLASS[k]" aria-hidden="true" />{{ t(`banquet.tone.${k}`) }}</li>
      <li class="flex items-center gap-1.5" data-testid="legend-lock"><span class="inline-block size-3 rounded-sm border border-dashed border-primary/60" :style="{ backgroundImage: HATCH }" aria-hidden="true" />{{ t('banquet.lock.legend') }}</li>
    </ul>

    <EmptyState v-if="loaded && !rooms.length" :title="t('banquet.calendar.noRooms')" :description="t('emptyState.banquetCalendar')" />

    <template v-else-if="loaded">
      <!-- phone: the day as a list in time order -->
      <div class="md:hidden" data-testid="calendar-agenda">
        <p v-if="view === 'week'" class="muted">{{ t('banquet.calendar.weekOnWide') }}</p>
        <EmptyState v-if="!dayAgenda.length" :title="t('banquet.calendar.dayEmpty')" :description="t('emptyState.banquetDay')" />
        <ul v-else class="m-0 flex list-none flex-col gap-2 p-0">
          <li v-for="a in dayAgenda" :key="a.key">
            <RouterLink v-if="a.session" :to="`/banquet/events/${a.session.event_id}`" class="flex min-h-11 flex-col gap-0.5 rounded-md border p-3 no-underline" :class="TONE_CLASS[toneOf(a.session)]" :data-testid="`agenda-${a.session.session_id}`">
              <span class="font-medium">{{ hours5(a.session) }} · {{ a.session.room_name }}</span>
              <span>{{ roomLabel(a.session) }} · {{ a.session.session_name }}</span>
              <span class="text-xs">{{ toneKey(a.session) }}<template v-if="toneOf(a.session) === 'queued'"> #{{ a.session.rank }}</template> · {{ t('banquet.session.paxCount', { n: a.session.pax_estimated }) }}</span>
            </RouterLink>
            <RouterLink
              v-else-if="a.lock" :to="`/banquet/events/${a.lock.event_id}`" class="flex min-h-11 flex-col gap-0.5 rounded-md border border-dashed p-3 text-sm no-underline" :class="LOCK_CLASS(a.lock)"
              :style="{ backgroundImage: HATCH }" :data-testid="`agenda-lock-${a.lock.session_id}-${a.lock.room_id}`"
            >
              <span class="font-medium">{{ lockSpanText(a.lock) }} · {{ roomName(a.lock.room_id) }}</span>
              <span>{{ lockText(a.lock) }}</span>
            </RouterLink>
          </li>
        </ul>
      </div>

      <!-- day: a column per room -->
      <div v-if="view === 'day'" class="hidden overflow-x-auto md:block" data-testid="calendar-day">
        <div class="grid min-w-[40rem]" :style="{ gridTemplateColumns: `3.5rem repeat(${rooms.length}, minmax(9rem, 1fr))` }">
          <div />
          <div v-for="r in rooms" :key="r.id" class="border-b border-border px-2 py-1.5 text-sm font-medium" :data-testid="`room-head-${r.code}`">{{ r.name }}</div>
          <div class="relative" :style="{ height: `${gridHeight}px` }">
            <span v-for="h in hours" :key="h" class="absolute right-1 -translate-y-1/2 text-xs text-muted-foreground" :style="{ top: `${((h - hours[0]!) / hours.length) * 100}%` }">{{ hhmm(h * 60) }}</span>
          </div>
          <div
            v-for="r in rooms" :key="r.id" class="relative border-l border-border" :class="canManage ? 'cursor-cell' : ''" :style="{ height: `${gridHeight}px`, backgroundImage: `repeating-linear-gradient(to bottom, transparent 0, transparent ${HOUR_PX - 1}px, var(--color-border) ${HOUR_PX - 1}px, var(--color-border) ${HOUR_PX}px)` }"
            :data-testid="`room-col-${r.code}`" @click="slotClick($event, r.id, date)"
          >
            <RouterLink
              v-for="l in locksOf(locks, r.id, date)" :key="`l${l.session_id}`" :to="`/banquet/events/${l.event_id}`" class="absolute inset-x-1 flex items-start overflow-hidden rounded-md border border-dashed px-1.5 py-1 text-xs font-medium leading-tight no-underline"
              :class="LOCK_CLASS(l)" :style="lockStyle(l, date)" :data-testid="`lock-${l.session_id}-${r.code}`" :title="lockTitle(l)" @click.stop
            >{{ lockText(l) }}</RouterLink>
            <RouterLink
              v-for="s in sessionsOf(sessions, r.id, date)" :key="s.session_id" :to="`/banquet/events/${s.event_id}`"
              class="absolute flex flex-col overflow-hidden rounded-md border text-xs no-underline" :class="TONE_CLASS[toneOf(s)]"
              :style="blockStyle(s, lanesOf(r.id, date).get(s.session_id))" :data-testid="`block-${s.session_id}`" :data-tone="toneOf(s)" :data-lanes="lanesOf(r.id, date).get(s.session_id)?.lanes ?? 1" :title="blockLabel(s)" @click.stop
            >
              <span class="block border-b border-dashed border-current/40 bg-foreground/5" :style="{ height: `${(geometry(s, range.from, range.to).setupHeight / geometry(s, range.from, range.to).height) * 100}%` }" :data-testid="`setup-${s.session_id}`" />
              <span class="min-h-0 flex-1 overflow-hidden px-1.5 py-1 leading-tight"><span class="font-medium">{{ blockLabel(s) }}</span></span>
              <span class="block border-t border-dashed border-current/40 bg-foreground/5" :style="{ height: `${(geometry(s, range.from, range.to).teardownHeight / geometry(s, range.from, range.to).height) * 100}%` }" :data-testid="`teardown-${s.session_id}`" />
            </RouterLink>
          </div>
        </div>
      </div>

      <!-- week: a row per room -->
      <div v-else class="hidden overflow-x-auto md:block" data-testid="calendar-week">
        <table class="w-full min-w-[48rem] table-fixed border-collapse text-sm">
          <thead>
            <tr>
              <th class="w-32 border-b border-border p-2 text-left font-medium" scope="col">{{ t('banquet.session.room') }}</th>
              <th v-for="d in days" :key="d" class="border-b border-l border-border p-2 text-left font-medium" scope="col" :data-testid="`week-head-${d}`">
                <button type="button" class="cursor-pointer border-0 bg-transparent p-0 font-medium text-primary hover:underline" @click="date = d; view = 'day'">{{ formatDateWeekday(d) }}</button>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in rooms" :key="r.id" :data-testid="`week-row-${r.code}`">
              <th class="border-b border-border p-2 text-left font-medium" scope="row">{{ r.name }}</th>
              <td v-for="d in days" :key="d" class="h-16 border-b border-l border-border p-1 align-top" :class="canManage ? 'cursor-cell' : ''" @click="bookAt(r.id, d)">
                <RouterLink
                  v-for="l in locksOf(locks, r.id, d)" :key="`l${l.session_id}`" :to="`/banquet/events/${l.event_id}`" class="mb-1 block truncate rounded border border-dashed px-1.5 py-0.5 text-xs no-underline"
                  :class="LOCK_CLASS(l)" :style="{ backgroundImage: HATCH }" :data-testid="`chip-lock-${l.session_id}-${r.code}`" :title="lockTitle(l)" @click.stop
                >{{ lockText(l) }}</RouterLink>
                <RouterLink
                  v-for="s in sessionsOf(sessions, r.id, d)" :key="s.session_id" :to="`/banquet/events/${s.event_id}`" class="mb-1 block truncate rounded border px-1.5 py-0.5 text-xs no-underline"
                  :class="TONE_CLASS[toneOf(s)]" :data-testid="`chip-${s.session_id}`" :title="blockLabel(s)" @click.stop
                >{{ (s.start_time ?? '').slice(0, 5) }} {{ s.event_name }}<template v-if="toneOf(s) === 'queued'"> #{{ s.rank }}</template></RouterLink>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </template>

  <BanquetEventDialog v-if="pid !== null" v-model:open="eventOpen" :property-id="pid" :types="types" @saved="onEventSaved" />
  <BanquetSessionDialog v-if="pid !== null && created" v-model:open="sessionOpen" :property-id="pid" :event="created" :rooms="calendar?.rooms ?? []" :prefill="prefill" @saved="onSessionSaved" />
</template>
