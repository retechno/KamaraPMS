<script setup lang="ts">
import { BellOff, ChevronsUp, RefreshCw, Search, Sparkles } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatBusinessDate } from '@/utils/dates'

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

const visible = computed(() => {
  const q = search.value.trim().toLowerCase()
  return rooms.value.filter((r) => {
    if (occupancyFilter.value === 'BLOCKED' ? !r.block : occupancyFilter.value !== 'ALL' && r.occupancy !== occupancyFilter.value) return false
    if (housekeepingFilter.value !== 'ALL' && r.status !== housekeepingFilter.value) return false
    if (typeFilter.value && String(r.room_type_id) !== typeFilter.value) return false
    return !q || r.room_number.toLowerCase().includes(q)
  })
})

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
  { value: 'ALL' as const, label: t('roomStatus.all') },
  { value: 'DIRTY' as const, label: t('status.DIRTY') },
  { value: 'CLEANING' as const, label: t('status.CLEANING') },
  { value: 'CLEAN' as const, label: t('status.CLEAN') },
  { value: 'INSPECTED' as const, label: t('status.INSPECTED') },
])

// The colour bar on the left of a tile says whose room it is today; a blocked room is greyed.
const tileClass = (r: HousekeepingBoardRoom): string =>
  r.block
    ? 'border-l-destructive bg-muted/70'
    : r.occupancy === 'OCCUPIED'
      ? 'border-l-primary bg-primary/5'
      : r.occupancy === 'RESERVED'
        ? 'border-l-warning bg-warning/5'
        : 'border-l-border'

const legend = [
  { cls: 'bg-primary', key: 'OCCUPIED' },
  { cls: 'bg-warning', key: 'RESERVED' },
  { cls: 'bg-border', key: 'VACANT' },
  { cls: 'bg-destructive', key: 'BLOCKED' },
] as const

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

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('roomStatus.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('roomStatus.noAccess') }}</p>

  <template v-else-if="loaded">
    <div class="mb-4 flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
      <p class="m-0 text-muted-foreground" data-testid="counts">
        {{ t('roomStatus.counts', { occupied: counts.occupied, reserved: counts.reserved, vacant: counts.vacant, blocked: counts.blocked }) }}
      </p>
      <ul class="m-0 flex list-none flex-wrap gap-3 p-0 text-xs text-muted-foreground" data-testid="legend">
        <li v-for="l in legend" :key="l.key" class="flex items-center gap-1.5">
          <span :class="cn('h-3 w-1 rounded-sm', l.cls)" aria-hidden="true" />{{ l.key === 'BLOCKED' ? t('roomStatus.blocked') : t(`status.${l.key}` as never) }}
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
          {{ c.label }}
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
    </div>

    <EmptyState v-if="!visible.length" :title="t('roomStatus.noMatch')" data-testid="no-match" />
    <section v-for="[floor, list] in floors" :key="floor" class="mb-6">
      <h2 class="mb-2 text-sm font-semibold text-muted-foreground">{{ floor ? t('roomStatus.floor', { floor }) : t('roomStatus.noFloor') }}</h2>
      <div class="grid grid-cols-[repeat(auto-fill,minmax(7.5rem,1fr))] gap-2">
        <button
          v-for="r in list"
          :key="r.room_id"
          type="button"
          :data-testid="`tile-${r.room_number}`"
          :class="cn('flex cursor-pointer flex-col items-start gap-1 rounded-lg border border-l-4 border-border bg-card p-2.5 text-left text-foreground shadow-sm transition-shadow hover:shadow-md focus-visible:ring-2 focus-visible:ring-ring', tileClass(r))"
          @click="selected = r"
        >
          <span class="flex w-full items-start justify-between gap-1">
            <strong class="text-lg leading-none">{{ r.room_number }}</strong>
            <span class="flex items-center gap-0.5 text-muted-foreground">
              <BellOff v-if="r.dnd" class="size-3.5" :title="t('roomStatus.dnd')" role="img" :aria-label="t('roomStatus.dnd')" />
              <Sparkles v-if="r.make_up_requested" class="size-3.5" :title="t('roomStatus.makeUp')" role="img" :aria-label="t('roomStatus.makeUp')" />
              <ChevronsUp v-if="r.priority === 'HIGH'" class="size-3.5 text-warning" :title="t('roomStatus.highPriority')" role="img" :aria-label="t('roomStatus.highPriority')" />
            </span>
          </span>
          <small class="text-muted-foreground">{{ r.room_type_code }}</small>
          <StatusBadge domain="occupancy" :status="r.occupancy" :data-testid="`occ-${r.room_number}`" />
          <StatusBadge domain="housekeeping" :status="r.status" />
          <Badge v-if="r.block" variant="destructive">{{ t(`roomStatus.${r.block.type}` as never) }}</Badge>
        </button>
      </div>
    </section>
  </template>

  <div v-else-if="canRead && property.currentId !== null && !error" class="grid grid-cols-[repeat(auto-fill,minmax(7.5rem,1fr))] gap-2" data-testid="loading">
    <Skeleton v-for="n in 12" :key="n" class="h-28" />
  </div>

  <Sheet v-model:open="sheetOpen">
    <SheetContent v-if="selected" class="p-6" data-testid="room-sheet">
      <SheetTitle class="text-xl">{{ t('roomStatus.details', { number: selected.room_number }) }}</SheetTitle>
      <SheetDescription class="mt-1">{{ selected.room_type_code }} · {{ selected.room_type_name }}</SheetDescription>
      <div class="mt-4 flex flex-wrap gap-2">
        <StatusBadge domain="occupancy" :status="selected.occupancy" />
        <StatusBadge domain="housekeeping" :status="selected.status" />
        <Badge v-if="selected.block" variant="destructive">{{ t(`roomStatus.${selected.block.type}` as never) }}</Badge>
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
