<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosKitchenTicket, PosStation, PosStationBoard } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { debounce, usePosEvents } from '@/composables/usePosEvents'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatTime } from '@/utils/format'

/**
 * The kitchen display (docs/architecture/34-pos.md 10.2, mockup 14): the tickets of one station in the columns New, Cooking and Ready, oldest first, with the
 * time they have waited, the late ones marked, a voided line struck through and the rounds after the first marked as an addition. It is read-only about money.
 * A tap moves a ticket on; the screen follows the stream of the station, so another screen's tap shows here by itself.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const STORAGE_KEY = 'kamarapms.posStation'

const stations = ref<PosStation[]>([])
const stationId = ref<number | null>(null)
const board = ref<PosStationBoard | null>(null)
const error = ref<ApiError | null>(null)
const loaded = ref(false)
const busyTicket = ref<number | null>(null)
const showHistory = ref(false)
const fetchedAt = ref(Date.now())
const tick = ref(Date.now())

const station = computed(() => stations.value.find((s) => s.id === stationId.value) ?? null)

async function loadStations(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/stations', { params: { path: { propertyId } } })
    stations.value = (data?.data ?? []).filter((s) => s.is_active && s.kds_enabled)
    let want = 0
    try {
      want = Number(localStorage.getItem(`${STORAGE_KEY}.${propertyId}`))
    } catch {
      want = 0
    }
    stationId.value = stations.value.find((s) => s.id === want)?.id ?? stations.value[0]?.id ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function loadBoard(): Promise<void> {
  const propertyId = pid.value
  const id = stationId.value
  if (propertyId === null || id === null) {
    board.value = null
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/stations/{id}/board', { params: { path: { propertyId, id } } })
    if (stationId.value === id) {
      board.value = data ?? null
      fetchedAt.value = Date.now()
    }
    error.value = null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
const refresh = debounce(() => void loadBoard(), 120)

watch(pid, () => void loadStations(), { immediate: true })
watch(stationId, (id) => {
  board.value = null
  if (id !== null) {
    try {
      localStorage.setItem(`${STORAGE_KEY}.${pid.value}`, String(id))
    } catch {
      // private window: the choice is not remembered
    }
  }
  void loadBoard()
})

const events = usePosEvents({
  propertyId: () => pid.value,
  channels: () => (stationId.value === null ? [] : [`station:${stationId.value}`]),
  onEvent: () => refresh(),
  onReload: () => void loadBoard(),
})

const clock = setInterval(() => {
  tick.value = Date.now()
}, 15_000)
onBeforeUnmount(() => clearInterval(clock))

const minutes = (tk: PosKitchenTicket): number => Math.max(0, Math.floor((tk.wait_seconds + Math.max(0, tick.value - fetchedAt.value) / 1000) / 60))
const lateAfter = computed(() => station.value?.late_after_minutes ?? 15)
const isLate = (tk: PosKitchenTicket): boolean => (tk.status === 'NEW' || tk.status === 'COOKING') && minutes(tk) >= lateAfter.value
const columns = computed(() => {
  const tickets = board.value?.tickets ?? []
  return (['NEW', 'COOKING', 'READY'] as const).map((s) => ({ status: s, tickets: tickets.filter((x) => x.status === s) }))
})
const lateCount = computed(() => (board.value?.tickets ?? []).filter(isLate).length)
const averageMinutes = computed(() => Math.round((board.value?.average_wait_seconds ?? 0) / 60))
const outletsText = computed(() => (board.value?.outlets ?? []).map((o) => o.name).join(', '))

function where(tk: PosKitchenTicket): string {
  if (tk.table_code) return t('posKds.table', { code: tk.table_code })
  if (tk.room_number) return t('posKds.room', { room: tk.room_number })
  return tk.customer_name || t(`posKds.type_${tk.order_type}`)
}
const source = (tk: PosKitchenTicket): string => [tk.bill_number, tk.waiter, tk.room_number && tk.table_code ? '' : tk.outlet_name].filter(Boolean).join(' · ')
const NEXT = { NEW: 'START', COOKING: 'READY', READY: 'SERVE' } as const
const LABEL = { NEW: 'posKds.start', COOKING: 'posKds.ready', READY: 'posKds.served' } as const

async function advance(tk: PosKitchenTicket, action?: 'START' | 'READY' | 'SERVE'): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || busyTicket.value !== null) return
  const act = action ?? NEXT[tk.status as 'NEW' | 'COOKING' | 'READY']
  busyTicket.value = tk.id
  try {
    await api.POST('/api/v1/properties/{propertyId}/pos/tickets/{id}/status', { params: { path: { propertyId, id: tk.id } }, body: { action: act } })
    error.value = null
  } catch (e) {
    // another screen got there first, or it has moved on: the board says what is true now
    error.value = e instanceof ApiError && e.code !== 'TICKET_STATUS_INVALID' ? e : null
  } finally {
    busyTicket.value = null
    await loadBoard()
  }
}
</script>

<template>
  <PageHeader :title="t('posKds.title')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <EmptyState v-else-if="loaded && stations.length === 0" :title="t('posKds.noStation')" :description="t('posKds.noStationHint')" data-testid="kds-no-station" />

  <section v-else-if="station" class="rounded-2xl bg-[#14181d] p-4 text-[#f3f5f7]" data-testid="kds" data-theme-kds>
    <header class="mb-4 flex flex-wrap items-center gap-3">
      <div class="flex flex-wrap gap-2" role="tablist" :aria-label="t('posKds.station')">
        <button
          v-for="s in stations" :key="s.id" type="button" role="tab" :aria-selected="s.id === stationId" :data-testid="`station-${s.id}`"
          class="min-h-11 cursor-pointer rounded-full border px-4 text-sm font-semibold"
          :class="s.id === stationId ? 'border-[#7fd0b9] bg-[#1d3a33] text-[#a9e2d2]' : 'border-[#3a414b] bg-[#1d2229] text-[#c9d1da]'"
          @click="stationId = s.id"
        >{{ s.name }}</button>
      </div>
      <span class="text-sm text-[#aeb7c2]" data-testid="kds-outlets">{{ outletsText }}</span>
      <span class="ml-auto flex items-center gap-3 text-sm text-[#aeb7c2]">
        <span data-testid="kds-summary">{{ t('posKds.summary', { avg: averageMinutes, late: lateCount }) }}</span>
        <span
          class="inline-flex items-center gap-1 text-xs" :data-testid="`kds-state-${events.state.value}`"
          :class="events.state.value === 'live' ? 'text-[#7fd0b9]' : 'text-[#f0a35e]'"
        >
          <span class="size-2 rounded-full" :class="events.state.value === 'live' ? 'bg-[#7fd0b9]' : 'bg-[#f0a35e]'" aria-hidden="true" />
          {{ t(`posKds.stream_${events.state.value}`) }}
        </span>
        <span v-if="board" class="tabular-nums text-base font-semibold text-[#f3f5f7]">{{ formatTime(board.now) }}</span>
      </span>
    </header>

    <div class="grid gap-4 lg:grid-cols-3">
      <div v-for="col in columns" :key="col.status" class="flex min-w-0 flex-col gap-3" :data-testid="`col-${col.status}`">
        <h2 class="m-0 flex items-center gap-2 text-sm font-bold uppercase tracking-wider text-[#c9d1da]">
          {{ t(`posKds.col_${col.status}`) }} <span class="text-[#aeb7c2]">· {{ col.tickets.length }}</span>
        </h2>
        <p v-if="col.tickets.length === 0" class="m-0 rounded-xl border border-dashed border-[#3a414b] p-6 text-center text-sm text-[#aeb7c2]">{{ t('posKds.empty') }}</p>
        <article
          v-for="tk in col.tickets" :key="tk.id" class="flex flex-col overflow-hidden rounded-[14px] border bg-[#1d2229]"
          :class="isLate(tk) ? 'border-[#e5564f]' : 'border-[#2f3640]'" :data-testid="`ticket-${tk.id}`" :data-late="isLate(tk)"
        >
          <header class="flex items-center gap-3 border-b border-[#2f3640] px-4 py-3">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-x-2 text-xl font-bold">
                {{ where(tk) }}
                <span v-if="tk.is_addition" class="rounded-md bg-[#f0a35e]/20 px-1.5 py-0.5 text-xs font-bold uppercase tracking-wide text-[#f0a35e]" data-testid="addition">{{ t('posKds.addition') }}</span>
              </div>
              <div class="break-words text-xs text-[#aeb7c2]">{{ source(tk) }}</div>
            </div>
            <div class="text-right">
              <div class="text-base font-semibold tabular-nums" :class="isLate(tk) ? 'text-[#ff8a84]' : 'text-[#f3f5f7]'" data-testid="age">
                <template v-if="tk.status === 'READY'">{{ minutes(tk) < 1 ? t('posKds.justNow') : t('posKds.readyAgo', { n: minutes(tk) }) }}</template>
                <template v-else>{{ t('posKds.minutes', { n: minutes(tk) }) }}</template>
              </div>
              <div v-if="isLate(tk)" class="text-xs font-bold uppercase text-[#ff8a84]" data-testid="late">{{ t('posKds.late') }}</div>
            </div>
          </header>
          <ul class="m-0 list-none p-0">
            <li v-for="l in tk.lines" :key="l.id" class="flex gap-3 border-t border-[#262c34] px-4 py-2.5 text-base first:border-t-0" :data-testid="`tline-${l.id}`" :data-void="l.is_void">
              <span class="w-7 shrink-0 font-bold" :class="l.is_void ? 'text-[#aeb7c2] line-through' : ''">{{ Number(l.quantity) }}</span>
              <span class="min-w-0 flex-1">
                <span :class="l.is_void ? 'text-[#aeb7c2] line-through' : ''">{{ l.name }}</span>
                <span v-if="l.is_void" class="ml-2 text-xs font-bold text-[#ff8a84]">{{ t('posKds.cancelled') }}</span>
                <span v-if="l.options || l.note" class="block text-[13px] text-[#f0a35e]" :class="l.is_void ? 'line-through opacity-60' : ''">{{ [l.options, l.note].filter(Boolean).join(' · ') }}</span>
              </span>
            </li>
          </ul>
          <footer class="flex gap-2 border-t border-[#2f3640] p-3">
            <button
              v-if="tk.status === 'NEW'" type="button" class="h-12 flex-1 cursor-pointer rounded-[10px] border border-[#3a414b] bg-[#262b33] text-[15px] font-bold text-[#f3f5f7] disabled:opacity-50"
              :disabled="busyTicket !== null" :data-testid="`ready-${tk.id}`" @click="advance(tk, 'READY')"
            >{{ t('posKds.ready') }}</button>
            <button
              type="button" class="h-12 flex-1 cursor-pointer rounded-[10px] border border-[#7fd0b9] bg-[#1f6f5c] text-[15px] font-bold text-white disabled:opacity-50"
              :disabled="busyTicket !== null" :data-testid="`next-${tk.id}`" @click="advance(tk)"
            >{{ t(LABEL[tk.status as 'NEW' | 'COOKING' | 'READY']) }}</button>
          </footer>
        </article>
      </div>
    </div>

    <footer class="mt-4 border-t border-[#2f3640] pt-3 text-sm text-[#aeb7c2]">
      <button type="button" class="min-h-11 cursor-pointer border-0 bg-transparent p-0 text-left text-[#7fd0b9] underline-offset-2 hover:underline" data-testid="kds-history-toggle" :aria-expanded="showHistory" @click="showHistory = !showHistory">
        {{ t('posKds.historyLine', { n: board?.history.length ?? 0 }) }}
      </button>
      <ul v-if="showHistory" class="m-0 mt-2 list-none p-0" data-testid="kds-history">
        <li v-for="tk in board?.history ?? []" :key="tk.id" class="flex flex-wrap gap-x-3 border-t border-[#262c34] py-2 text-[#c9d1da]" :data-testid="`history-${tk.id}`">
          <strong>{{ where(tk) }}</strong>
          <span>{{ tk.bill_number }}</span>
          <span class="text-[#aeb7c2]">{{ tk.lines.filter((l) => !l.is_void).map((l) => `${Number(l.quantity)} × ${l.name}`).join(', ') }}</span>
          <span v-if="tk.served_at" class="ml-auto tabular-nums">{{ formatTime(tk.served_at) }}</span>
        </li>
        <li v-if="(board?.history.length ?? 0) === 0" class="py-2">{{ t('posKds.noHistory') }}</li>
      </ul>
    </footer>
  </section>
</template>
