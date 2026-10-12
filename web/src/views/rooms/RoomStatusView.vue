<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { BellOff, Check, ChevronsUp, RefreshCw, Search, Sparkles, X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom, HousekeepingBulkResult, HousekeepingStatus } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { statusLegend, statusSwatch } from '@/components/app/statusMap'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { confirm } from '@/composables/useConfirm'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatBusinessDate } from '@/utils/dates'
import { statusText } from '@/utils/status'

const auth = useAuthStore()
const property = usePropertyStore()

const rooms = ref<HousekeepingBoardRoom[]>([])
const error = ref<ApiError | null>(null)
const loaded = ref(false)
const busy = ref(false)

const occupancyFilter = ref<'ALL' | 'OCCUPIED' | 'RESERVED' | 'VACANT' | 'BLOCKED'>('ALL')
const housekeepingFilter = ref<'ALL' | 'DIRTY' | 'CLEANING' | 'CLEAN' | 'INSPECTED'>('ALL')
const typeFilter = ref<string>('')
const search = ref('')
const selected = ref<HousekeepingBoardRoom | null>(null)

const canRead = computed(() => auth.can('housekeeping.read', property.currentId) || auth.can('reservation.read', property.currentId))

// Changing the cleaning of many rooms at once: housekeeping.update; "Inspected" also needs housekeeping.inspect (the same rule as the single change).
const canChange = computed(() => auth.can('housekeeping.update', property.currentId))
const canInspect = computed(() => canChange.value && auth.can('housekeeping.inspect', property.currentId))

const counts = computed(() => ({
  occupied: rooms.value.filter((r) => r.occupancy === 'OCCUPIED').length,
  reserved: rooms.value.filter((r) => r.occupancy === 'RESERVED').length,
  vacant: rooms.value.filter((r) => r.occupancy === 'VACANT').length,
  blocked: rooms.value.filter((r) => r.block).length,
}))

const roomTypes = computed(() => {
  const seen = new Map<number, string>()
  for (const r of rooms.value) seen.set(r.room_type_id, r.room_type_code)
  return [...seen.entries()].sort((a, b) => a[1].localeCompare(b[1]))
})

// The rooms that pass the filters other than the cleaning chip: what the chips count (so the number of a chip is what the board shows when it is pressed).
const beforeCleaning = computed(() => {
  const q = search.value.trim().toLowerCase()
  return rooms.value.filter((r) => {
    if (occupancyFilter.value === 'BLOCKED' ? !r.block : occupancyFilter.value !== 'ALL' && r.occupancy !== occupancyFilter.value) return false
    if (typeFilter.value && String(r.room_type_id) !== typeFilter.value) return false
    return !q || r.room_number.toLowerCase().includes(q)
  })
})
const visible = computed(() => beforeCleaning.value.filter((r) => housekeepingFilter.value === 'ALL' || r.status === housekeepingFilter.value))
const cleaningCount = (status: 'ALL' | HousekeepingStatus): number => (status === 'ALL' ? beforeCleaning.value.length : beforeCleaning.value.filter((r) => r.status === status).length)

const floors = computed(() => {
  const by = new Map<string, HousekeepingBoardRoom[]>()
  for (const r of visible.value) by.set(r.floor ?? '', [...(by.get(r.floor ?? '') ?? []), r])
  return [...by.entries()].sort(([a], [b]) => a.localeCompare(b))
})

const occupancyChips = computed(() => [
  { value: 'ALL' as const, label: t('roomStatus.all') },
  { value: 'OCCUPIED' as const, label: t('status.OCCUPIED') },
  { value: 'RESERVED' as const, label: t('status.RESERVED') },
  { value: 'VACANT' as const, label: t('status.VACANT') },
  { value: 'BLOCKED' as const, label: t('roomStatus.blocked') },
])
const housekeepingChips = computed(() => [
  { value: 'ALL' as const, label: t('roomStatus.all'), count: cleaningCount('ALL') },
  { value: 'DIRTY' as const, label: t('status.DIRTY'), count: cleaningCount('DIRTY') },
  { value: 'CLEANING' as const, label: t('status.CLEANING'), count: cleaningCount('CLEANING') },
  { value: 'CLEAN' as const, label: t('status.CLEAN'), count: cleaningCount('CLEAN') },
  { value: 'INSPECTED' as const, label: t('status.INSPECTED'), count: cleaningCount('INSPECTED') },
])

/**
 * What a tile looks like. The colour of a tile is the cleaning of the room, and it is strongest where it matters: a room that is vacant or reserved is filled with the colour of
 * its cleaning, so a dirty room that guests are about to arrive in cannot be missed; a blocked room is the striped grey of "not available". A room with a guest in it is a white
 * tile: right after night audit nearly every occupied room is dirty, which is normal and not urgent, so it only gets the colour bar on its left and a small chip with the cleaning.
 */
interface TileLook {
  neutral: boolean
  cls: string
}
function tileLook(r: HousekeepingBoardRoom): TileLook {
  if (r.block) return { neutral: false, cls: statusSwatch('block', r.block.type).fill }
  const hk = statusSwatch('housekeeping', r.status)
  if (r.occupancy === 'OCCUPIED') return { neutral: true, cls: cn('border-l-4 border-border bg-card text-foreground', hk.edge) }
  return { neutral: false, cls: hk.fill }
}

// The legend is the map the tiles are drawn from: the five cleaning states, then what a white tile means.
const legend = computed(() => statusLegend('housekeeping').map((e) => ({ ...e, label: e.status === 'BLOCKED' ? t('roomStatus.blocked') : statusText(e.status) })))

// ---- Selecting rooms and changing their cleaning together ----

/** One request takes this many rooms (the limit of the API); a longer selection goes in several requests. */
const BULK_MAX = 100
/** More rooms than this are asked about before they are changed. */
const CONFIRM_ABOVE = 10

const picked = ref<number[]>([])
const bulkBusy = ref(false)
/** `same`: rooms that already had the status (neutral); `skipped`: rooms the rules or the data refused. */
const outcome = ref<{ changed: number; failed: number; same: HousekeepingBulkResult[]; skipped: HousekeepingBulkResult[]; to: HousekeepingStatus } | null>(null)
const isSame = (r: HousekeepingBulkResult, to: HousekeepingStatus): boolean => r.error?.code === 'INVALID_HK_TRANSITION' && r.from === to
const isPicked = (r: HousekeepingBoardRoom): boolean => picked.value.includes(r.room_id)
function togglePick(r: HousekeepingBoardRoom): void {
  picked.value = isPicked(r) ? picked.value.filter((id) => id !== r.room_id) : [...picked.value, r.room_id]
}
// A tile opens its details, until a room is selected: then a click on a tile selects (or unselects) it, as in the design.
function onTile(r: HousekeepingBoardRoom): void {
  if (canChange.value && picked.value.length) togglePick(r)
  else selected.value = r
}
const allShownPicked = computed(() => visible.value.length > 0 && visible.value.every(isPicked))
function toggleAllShown(): void {
  picked.value = allShownPicked.value ? [] : visible.value.map((r) => r.room_id)
}
const floorPicked = (list: HousekeepingBoardRoom[]): boolean => list.every(isPicked)
function toggleFloor(list: HousekeepingBoardRoom[]): void {
  const ids = list.map((r) => r.room_id)
  picked.value = floorPicked(list) ? picked.value.filter((id) => !ids.includes(id)) : [...new Set([...picked.value, ...ids])]
}
// A room that a filter hides is not selected any more: what the bar acts on is what is on the screen.
watch(visible, (list) => {
  const shown = new Set(list.map((r) => r.room_id))
  if (picked.value.some((id) => !shown.has(id))) picked.value = picked.value.filter((id) => shown.has(id))
})

/** The manual changes the server allows (the same table as `checkTransition` in internal/housekeeping): used to count what a button WILL change. The server still decides. */
const NEXT: Partial<Record<HousekeepingStatus, HousekeepingStatus[]>> = {
  DIRTY: ['CLEANING', 'CLEAN'],
  CLEANING: ['CLEAN', 'DIRTY'],
  CLEAN: ['INSPECTED', 'DIRTY'],
  INSPECTED: ['DIRTY'],
}
const pickedRooms = computed(() => rooms.value.filter((r) => picked.value.includes(r.room_id)))
/** How many of the selected rooms a button changes: the ones that already have the status, or cannot go there, are not counted. */
const willChange = (to: HousekeepingStatus): number => pickedRooms.value.filter((r) => r.status !== to && (NEXT[r.status] ?? []).includes(to)).length
const alreadyIn = (to: HousekeepingStatus): number => pickedRooms.value.filter((r) => r.status === to).length

const barActions = computed(() => {
  const mk = (to: 'CLEAN' | 'DIRTY' | 'INSPECTED', key: 'markClean' | 'markDirty' | 'markInspected', testid: string, primary: boolean) => {
    const n = willChange(to)
    const status = statusText(to).toLowerCase()
    const hint = n > 0 ? '' : alreadyIn(to) === picked.value.length ? t('roomStatus.noneAll', { status }) : t('roomStatus.noneCan', { status })
    return { to, label: t(`roomStatus.${key}`, { n }), testid, primary, n, hint }
  }
  return [
    mk('CLEAN', 'markClean', 'bulk-clean', false),
    mk('DIRTY', 'markDirty', 'bulk-dirty', false),
    ...(canInspect.value ? [mk('INSPECTED', 'markInspected', 'bulk-inspected', true)] : []),
  ]
})

/** Why a room stayed as it was, in words: it already has the status, the rules do not allow the change, or the room is gone. */
function reason(r: HousekeepingBulkResult, to: HousekeepingStatus): string {
  if (r.error?.code === 'ROOM_NOT_FOUND') return t('roomStatus.reasonNotFound')
  if (r.error?.code === 'INVALID_HK_TRANSITION' && r.from) {
    return r.from === to
      ? t('roomStatus.reasonSame', { status: statusText(to).toLowerCase() })
      : t('roomStatus.reasonTransition', { from: statusText(r.from).toLowerCase(), to: statusText(to).toLowerCase() })
  }
  return t('roomStatus.reasonOther')
}
const outcomeSummary = computed(() => {
  const o = outcome.value
  if (!o) return ''
  return o.failed || o.same.length ? t('roomStatus.resultMixed', { changed: o.changed, same: o.same.length, failed: o.failed }) : t('roomStatus.resultDone', { changed: o.changed }, o.changed)
})

/**
 * Changes the selected rooms to a status. It asks first only when it matters: more than ten rooms, or rooms that were inspected going back to dirty (an INSPECTED room can only go to
 * DIRTY). What the server skipped is reported room by room and stays selected; what changed leaves the selection.
 */
async function applyBulk(to: HousekeepingStatus): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !picked.value.length || bulkBusy.value) return
  const ids = [...picked.value]
  // The question is about the rooms that will change, the number on the button.
  const changing = willChange(to)
  if (changing === 0) return
  const lowered = to === 'DIRTY' ? rooms.value.filter((r) => ids.includes(r.room_id) && r.status === 'INSPECTED').length : 0
  if (changing > CONFIRM_ABOVE || lowered > 0) {
    const lines = [
      ...(changing > CONFIRM_ABOVE ? [t('roomStatus.confirmMany', { max: CONFIRM_ABOVE })] : []),
      ...(lowered > 0 ? [t('roomStatus.confirmLowered', { n: lowered, status: statusText(to).toLowerCase() }, lowered)] : []),
    ]
    const yes = await confirm({
      title: t('roomStatus.confirmTitle', { n: changing, status: statusText(to).toLowerCase() }, changing),
      description: lines.join(' '),
      confirmLabel: t('roomStatus.confirmButton'),
      destructive: lowered > 0,
    })
    if (!yes) return
  }
  bulkBusy.value = true
  error.value = null
  outcome.value = null
  const changedIds = new Set<number>()
  const skipped: HousekeepingBulkResult[] = []
  const same: HousekeepingBulkResult[] = []
  try {
    for (let i = 0; i < ids.length; i += BULK_MAX) {
      const { data } = await api.POST('/api/v1/properties/{propertyId}/housekeeping/bulk', { params: { path: { propertyId } }, body: { room_ids: ids.slice(i, i + BULK_MAX), status: to } })
      for (const r of data?.results ?? []) {
        if (r.ok) changedIds.add(r.room_id)
        else if (isSame(r, to)) same.push(r)
        else skipped.push(r)
      }
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    bulkBusy.value = false
  }
  // What changed leaves the selection, and so do the rooms that already had the status (nothing is left to do for them); the rooms that were skipped, and the ones a refused request never reached, stay selected.
  const sameIds = new Set(same.map((r) => r.room_id))
  picked.value = ids.filter((id) => !changedIds.has(id) && !sameIds.has(id))
  outcome.value = { changed: changedIds.size, failed: skipped.length, same, skipped, to }
  const failure = error.value
  await load()
  error.value = failure
}

function clearFilters(): void {
  occupancyFilter.value = 'ALL'
  housekeepingFilter.value = 'ALL'
  typeFilter.value = ''
  search.value = ''
}

async function load(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  error.value = null
  busy.value = true
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping', { params: { path: { propertyId } } })
    rooms.value = data?.data ?? []
    loaded.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

const sheetOpen = computed({
  get: () => selected.value !== null,
  set: (v: boolean) => {
    if (!v) selected.value = null
  },
})

const updatedAt = (iso: string): string => new Date(iso).toLocaleString()

watch(() => property.currentId, () => {
  rooms.value = []
  loaded.value = false
  selected.value = null
  picked.value = []
  outcome.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('roomStatus.title')" :description="t('roomStatus.description')">
    <template #actions>
      <Button variant="outline" size="sm" :disabled="busy" data-testid="refresh" @click="load">
        <RefreshCw :class="busy && 'animate-spin'" />{{ t('common.refresh') }}
      </Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="property.currentId === null" class="muted">{{ t('roomStatus.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('roomStatus.noAccess') }}</p>

  <template v-else-if="loaded">
    <div class="mb-4 flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
      <p class="m-0 text-muted-foreground" data-testid="counts">
        {{ t('roomStatus.counts', { occupied: counts.occupied, reserved: counts.reserved, vacant: counts.vacant, blocked: counts.blocked }) }}
      </p>
      <ul class="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-xs text-muted-foreground" data-testid="legend">
        <li v-for="l in legend" :key="l.status" class="flex items-center gap-1.5" :data-legend="l.status">
          <span :class="cn('h-3 w-5 rounded-sm border', l.swatch)" aria-hidden="true" />{{ l.label }}
        </li>
        <li class="flex items-center gap-1.5" data-legend="OCCUPIED">
          <span class="h-3 w-5 rounded-sm border border-l-4 border-border border-l-muted-foreground bg-card" aria-hidden="true" />{{ t('roomStatus.legendOccupied') }}
        </li>
      </ul>
    </div>

    <div class="mb-5 flex flex-wrap items-center gap-x-4 gap-y-2" data-testid="filters">
      <div class="flex flex-wrap gap-1" role="group" :aria-label="t('roomStatus.filterOccupancy')">
        <Button
          v-for="c in occupancyChips"
          :key="c.value"
          size="sm"
          :variant="occupancyFilter === c.value ? 'default' : 'outline'"
          :aria-pressed="occupancyFilter === c.value"
          :data-testid="`filter-occ-${c.value}`"
          @click="occupancyFilter = c.value"
        >
          {{ c.label }}
        </Button>
      </div>
      <div class="flex flex-wrap gap-1" role="group" :aria-label="t('roomStatus.filterHousekeeping')">
        <Button
          v-for="c in housekeepingChips"
          :key="c.value"
          size="sm"
          :variant="housekeepingFilter === c.value ? 'default' : 'outline'"
          :aria-pressed="housekeepingFilter === c.value"
          :data-testid="`filter-hk-${c.value}`"
          @click="housekeepingFilter = c.value"
        >
          {{ c.label }}<span class="ml-1.5 font-bold" :data-testid="`count-hk-${c.value}`">{{ c.count }}</span>
        </Button>
      </div>
      <NativeSelect v-model="typeFilter" class="w-44" name="room_type" :aria-label="t('roomStatus.roomType')">
        <option value="">{{ t('roomStatus.allTypes') }}</option>
        <option v-for="[id, code] in roomTypes" :key="id" :value="String(id)">{{ code }}</option>
      </NativeSelect>
      <div class="relative">
        <Search class="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
        <Input v-model="search" class="w-44 pl-8" name="room_search" :placeholder="t('roomStatus.findRoom')" :aria-label="t('roomStatus.findRoom')" />
      </div>
      <Button v-if="canChange && visible.length" variant="outline" size="sm" :aria-pressed="allShownPicked" data-testid="pick-all" @click="toggleAllShown">
        {{ allShownPicked ? t('roomStatus.selectNone') : t('roomStatus.selectAll', { n: visible.length }) }}
      </Button>
    </div>

    <div v-if="outcome" class="mb-4 rounded-lg border border-border bg-card p-3 text-sm" role="status" data-testid="bulk-result">
      <div class="flex items-start justify-between gap-3">
        <p class="m-0 font-semibold" data-testid="bulk-result-summary">{{ outcomeSummary }}</p>
        <button type="button" class="cursor-pointer rounded border-0 bg-transparent p-1 text-muted-foreground hover:bg-muted" :aria-label="t('roomStatus.resultClose')" data-testid="bulk-result-close" @click="outcome = null"><X class="size-4" aria-hidden="true" /></button>
      </div>
      <details v-if="outcome.same.length" class="mt-2" data-testid="bulk-same-details">
        <summary class="cursor-pointer text-muted-foreground">{{ t('roomStatus.resultSame') }}</summary>
        <ul class="m-0 mt-1 list-none space-y-0.5 p-0 text-muted-foreground" data-testid="bulk-same">
          <li v-for="r in outcome.same" :key="r.room_id" :data-testid="`same-${r.room_number ?? r.room_id}`"><b>{{ r.room_number ?? r.room_id }}</b> · {{ reason(r, outcome.to) }}</li>
        </ul>
      </details>
      <details v-if="outcome.skipped.length" class="mt-2" open>
        <summary class="cursor-pointer text-muted-foreground">{{ t('roomStatus.resultSkipped') }}</summary>
        <ul class="m-0 mt-1 list-none space-y-0.5 p-0" data-testid="bulk-skipped">
          <li v-for="r in outcome.skipped" :key="r.room_id" :data-testid="`skipped-${r.room_number ?? r.room_id}`"><b>{{ r.room_number ?? r.room_id }}</b> · {{ reason(r, outcome.to) }}</li>
        </ul>
      </details>
    </div>

    <EmptyState v-if="!visible.length" :title="t('roomStatus.noMatch')" :description="t('emptyState.noMatch')" :action-label="t('dataTable.clearFilters')" action-variant="outline" data-testid="no-match" @action="clearFilters" />
    <section v-for="[floor, list] in floors" :key="floor" class="mb-6">
      <div class="mb-2 flex items-center gap-3">
        <h2 class="m-0 text-sm font-semibold text-muted-foreground">{{ floor ? t('roomStatus.floor', { floor }) : t('roomStatus.noFloor') }}</h2>
        <Button v-if="canChange" variant="ghost" size="sm" class="h-7 px-2 text-xs" :aria-pressed="floorPicked(list)" :data-testid="`pick-floor-${floor || 'none'}`" @click="toggleFloor(list)">
          {{ floorPicked(list) ? t('roomStatus.selectNone') : t('roomStatus.selectFloor', { floor: floor || t('roomStatus.noFloor') }) }}
        </Button>
      </div>
      <div class="grid grid-cols-[repeat(auto-fill,minmax(7.5rem,1fr))] gap-2">
        <div v-for="r in list" :key="r.room_id" class="relative">
        <button
          type="button"
          :data-testid="`tile-${r.room_number}`"
          :data-tile="tileLook(r).neutral ? 'neutral' : 'filled'"
          :data-picked="isPicked(r) ? 'true' : undefined"
          :class="cn('flex min-h-24 w-full cursor-pointer flex-col items-start justify-between gap-1 rounded-lg border p-2.5 text-left shadow-sm transition-shadow hover:shadow-md focus-visible:ring-2 focus-visible:ring-ring', tileLook(r).cls, isPicked(r) && 'ring-2 ring-primary ring-offset-2 ring-offset-background')"
          @click="onTile(r)"
        >
          <span class="flex w-full items-start justify-between gap-1">
            <span class="flex flex-col gap-0.5">
              <strong class="text-lg leading-none">{{ r.room_number }}</strong>
              <small class="font-semibold">{{ r.room_type_code }}</small>
            </span>
            <span :class="cn('flex items-center gap-0.5', canChange && 'pr-7')">
              <BellOff v-if="r.dnd" class="size-3.5" :title="t('roomStatus.dnd')" role="img" :aria-label="t('roomStatus.dnd')" />
              <Sparkles v-if="r.make_up_requested" class="size-3.5" :title="t('roomStatus.makeUp')" role="img" :aria-label="t('roomStatus.makeUp')" />
              <ChevronsUp v-if="r.priority === 'HIGH'" class="size-3.5" :title="t('roomStatus.highPriority')" role="img" :aria-label="t('roomStatus.highPriority')" />
            </span>
          </span>
          <StatusBadge domain="occupancy" :status="r.occupancy" class="bg-card text-foreground" :data-testid="`occ-${r.room_number}`" />
          <StatusBadge v-if="tileLook(r).neutral" domain="housekeeping" :status="r.status" data-slot="tile-cleaning" />
          <span v-else class="text-sm font-bold" data-slot="tile-cleaning">{{ r.block ? t(`roomStatus.${r.block.type}` as never) : statusText(r.status) }}</span>
        </button>
        <!-- The checkbox is next to the tile, not in it (a button cannot hold a button); the hit area is a 36 px square in the corner. -->
        <button
          v-if="canChange"
          type="button"
          class="absolute right-0 top-0 grid size-9 cursor-pointer place-items-center rounded-lg border-0 bg-transparent p-0 focus-visible:ring-2 focus-visible:ring-ring"
          :aria-pressed="isPicked(r)"
          :aria-label="t('roomStatus.selectRoom', { number: r.room_number })"
          :data-testid="`pick-${r.room_number}`"
          @click="togglePick(r)"
        >
          <span :class="cn('grid size-5 place-items-center rounded-md border-2 border-foreground/60 bg-card text-primary-foreground', isPicked(r) && 'border-primary bg-primary')"><Check v-if="isPicked(r)" class="size-3.5" stroke-width="3" aria-hidden="true" /></span>
        </button>
        </div>
      </div>
    </section>
    <div v-if="picked.length" class="h-24" aria-hidden="true" />
  </template>

  <div v-else-if="canRead && property.currentId !== null && !error" class="grid grid-cols-[repeat(auto-fill,minmax(7.5rem,1fr))] gap-2" data-testid="loading">
    <Skeleton v-for="n in 12" :key="n" class="h-28" />
  </div>

  <div
    v-if="canChange && picked.length"
    role="region"
    :aria-label="t('roomStatus.barLabel')"
    class="fixed inset-x-3 bottom-3 z-30 mx-auto flex max-w-3xl flex-wrap items-center gap-2 rounded-xl bg-foreground p-3 text-background shadow-xl"
    data-testid="bulk-bar"
  >
    <strong class="flex-1 px-1.5 text-sm" data-testid="bulk-count">{{ t('roomStatus.selectedCount', { n: picked.length }, picked.length) }}</strong>
    <Button
      v-for="a in barActions"
      :key="a.to"
      :variant="a.primary ? 'default' : 'secondary'"
      class="min-h-11"
      :disabled="bulkBusy || a.n === 0"
      :title="a.hint || undefined"
      :data-testid="a.testid"
      @click="applyBulk(a.to)"
    >{{ a.label }}</Button>
    <Button variant="ghost" class="min-h-11 text-background/80 hover:bg-background/10 hover:text-background" :disabled="bulkBusy" data-testid="bulk-cancel" @click="picked = []">{{ t('roomStatus.cancelSelection') }}</Button>
  </div>

  <Sheet v-model:open="sheetOpen">
    <SheetContent v-if="selected" class="p-6" data-testid="room-sheet">
      <SheetTitle class="text-xl">{{ t('roomStatus.details', { number: selected.room_number }) }}</SheetTitle>
      <SheetDescription class="mt-1">{{ selected.room_type_code }} · {{ selected.room_type_name }}</SheetDescription>
      <div class="mt-4 flex flex-wrap gap-2">
        <StatusBadge domain="occupancy" :status="selected.occupancy" />
        <StatusBadge domain="housekeeping" :status="selected.status" />
        <StatusBadge v-if="selected.block" domain="block" :status="selected.block.type" :label="t(`roomStatus.${selected.block.type}` as never)" />
      </div>
      <dl class="m-0 mt-4 divide-y divide-border text-sm">
        <div v-if="selected.floor" class="flex justify-between gap-3 py-2"><dt class="font-medium">{{ t('roomStatus.floorLabel') }}</dt><dd class="m-0">{{ selected.floor }}</dd></div>
        <div v-if="selected.building" class="flex justify-between gap-3 py-2"><dt class="font-medium">{{ t('roomStatus.building') }}</dt><dd class="m-0">{{ selected.building }}</dd></div>
        <div class="flex justify-between gap-3 py-2"><dt class="font-medium">{{ t('roomStatus.updated') }}</dt><dd class="m-0">{{ updatedAt(selected.status_updated_at) }}</dd></div>
        <div v-if="selected.allowed_next.length" class="flex items-center justify-between gap-3 py-2">
          <dt class="font-medium">{{ t('roomStatus.canBe') }}</dt>
          <dd class="m-0 flex flex-wrap justify-end gap-1"><StatusBadge v-for="s in selected.allowed_next" :key="s" domain="housekeeping" :status="s" /></dd>
        </div>
        <div v-if="selected.block" class="py-2" data-testid="sheet-block">{{ t('roomStatus.blockedUntil', { date: formatBusinessDate(selected.block.end_date) }) }}</div>
        <div v-if="selected.flag_note" class="py-2"><dt class="font-medium">{{ t('roomStatus.note') }}</dt><dd class="m-0 mt-1 text-muted-foreground">{{ selected.flag_note }}</dd></div>
      </dl>
      <Button as-child variant="outline" class="mt-5">
        <RouterLink to="/housekeeping" data-testid="sheet-housekeeping">{{ t('roomStatus.openHousekeeping') }}</RouterLink>
      </Button>
    </SheetContent>
  </Sheet>
</template>
