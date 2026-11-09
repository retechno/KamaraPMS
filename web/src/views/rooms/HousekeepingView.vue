<script setup lang="ts">
import FilterBar from '@/components/app/FilterBar.vue'
import type { RowAction } from '@/components/app/rowActions'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { BellOff, ChevronsUp, Lock, RefreshCw, Sparkles, StickyNote } from 'lucide-vue-next'
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom, HousekeepingStatus } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()
const router = useRouter()

const rooms = ref<HousekeepingBoardRoom[]>([])
const loading = ref(false)
const error = ref<ApiError | null>(null)
const busyRoom = ref<number | null>(null)
const STATUSES: HousekeepingStatus[] = ['DIRTY', 'CLEANING', 'CLEAN', 'INSPECTED']
// The status and the floor are in the address (?status=DIRTY&floor=3): a link from the home page lands on the list already narrowed, and a narrowed list can be shared.
const queryOf = (k: string): string => {
  const v = route.query[k]
  return typeof v === 'string' ? v : ''
}
const statusOf = (v: string): HousekeepingStatus | '' => ((STATUSES as string[]).includes(v) ? (v as HousekeepingStatus) : '')
const statusFilter = ref<HousekeepingStatus | ''>(statusOf(queryOf('status')))
const floorFilter = ref(queryOf('floor'))
const occupancyFilter = ref<'' | 'OCCUPIED' | 'RESERVED' | 'VACANT'>('')
const flaggedOnly = ref(false)
// The room whose flags are being edited.
const flagging = ref<HousekeepingBoardRoom | null>(null)
const flagSheet = ref<HTMLElement | null>(null)
const lastFlagged = ref('')
const flagForm = reactive({ priority: 'NORMAL' as 'NORMAL' | 'HIGH', dnd: false, make_up_requested: false, note: '' })
const savingFlags = ref(false)

const statuses = STATUSES
const label = (s: HousekeepingStatus): string => t(`housekeeping.${s.toLowerCase()}` as never)
const verb = (s: HousekeepingStatus): string => t(({ DIRTY: 'housekeeping.markDirty', CLEANING: 'housekeeping.startCleaning', CLEAN: 'housekeeping.markClean', INSPECTED: 'housekeeping.inspect' } as const)[s])

const canUpdate = computed(() => auth.can('housekeeping.update', property.currentId))
const canInspect = computed(() => auth.can('housekeeping.inspect', property.currentId))
const canReport = computed(() => auth.can('maintenance.report', property.currentId))

// The board is small by nature (one row per active room), so it is loaded whole and filtered here.
const floors = computed(() => [...new Set(rooms.value.map((r) => r.floor ?? '').filter(Boolean))].sort())
const hasFlag = (r: HousekeepingBoardRoom) => r.priority === 'HIGH' || r.dnd || r.make_up_requested || !!r.flag_note
// A number on a chip counts what the other filters leave: with floor 3 chosen, "Dirty 7" is the dirty rooms of floor 3 (the chips of status leave the status out, the chip of flags leaves the flags out).
const matches = (r: HousekeepingBoardRoom, skip: 'status' | 'flagged' | '' = ''): boolean =>
  (skip === 'status' || !statusFilter.value || r.status === statusFilter.value) &&
  (!floorFilter.value || r.floor === floorFilter.value) &&
  (!occupancyFilter.value || r.occupancy === occupancyFilter.value) &&
  (skip === 'flagged' || !flaggedOnly.value || hasFlag(r))
const statusPool = computed(() => rooms.value.filter((r) => matches(r, 'status')))
const counts = computed(() => Object.fromEntries(statuses.map((s) => [s, statusPool.value.filter((r) => r.status === s).length])))
const flaggedCount = computed(() => rooms.value.filter((r) => matches(r, 'flagged') && hasFlag(r)).length)
const visible = computed(() => rooms.value.filter((r) => matches(r)))

// The address follows the filters, and a link that changes the address (from the home page) moves the filters.
watch([statusFilter, floorFilter], () => {
  const query = { ...route.query }
  if (statusFilter.value) query.status = statusFilter.value
  else delete query.status
  if (floorFilter.value) query.floor = floorFilter.value
  else delete query.floor
  if (query.status !== route.query.status || query.floor !== route.query.floor) void router.replace({ query })
})
watch(
  () => [route.query.status, route.query.floor],
  () => {
    statusFilter.value = statusOf(queryOf('status'))
    floorFilter.value = queryOf('floor')
  },
)

const activeFilters = computed(() => (statusFilter.value ? 1 : 0) + (flaggedOnly.value ? 1 : 0) + (occupancyFilter.value ? 1 : 0) + (floorFilter.value ? 1 : 0))

function clearFilters(): void {
  statusFilter.value = ''
  flaggedOnly.value = false
  occupancyFilter.value = ''
  floorFilter.value = ''
}

const columns = computed<Column<HousekeepingBoardRoom>[]>(() => [
  { key: 'room_number', label: t('housekeeping.room'), sortable: true, card: 'primary' as const },
  { key: 'room_type_code', label: t('housekeeping.type'), sortable: true, card: 'secondary' as const },
  { key: 'status', label: t('housekeeping.housekeepingCol'), sortable: true, card: 'badge' as const },
  { key: 'occupancy', label: t('housekeeping.occupancyCol'), sortable: true },
  { key: 'notes', label: t('housekeeping.notes') },
  ...(canUpdate.value || canReport.value ? [{ key: 'actions', label: t('housekeeping.actions'), align: 'right' as const }] : []),
])

/** The one step that is offered as a button for a room in this status; every other step is in the "..." menu. */
const MAIN_STEP: Partial<Record<HousekeepingStatus, HousekeepingStatus>> = { DIRTY: 'CLEANING', CLEANING: 'CLEAN', CLEAN: 'INSPECTED' }

function actionsFor(room: HousekeepingBoardRoom): HousekeepingStatus[] {
  return room.allowed_next.filter((s) => s !== 'INSPECTED' || canInspect.value)
}

/**
 * The actions of a room, for the row of the table and the card of a phone alike: the next cleaning step is the one button (none for a blocked room or an
 * inspected one), the other steps, the note and the maintenance report are in the "..." menu. A room with nothing allowed has no actions at all.
 */
function roomActions(r: HousekeepingBoardRoom): RowAction[] {
  const acts: RowAction[] = []
  if (canUpdate.value) {
    const main = r.block ? undefined : MAIN_STEP[r.status]
    actionsFor(r).forEach((s) => acts.push({ key: `status-${s}`, label: verb(s), primary: s === main, disabled: busyRoom.value === r.room_id, testId: `act-${s}`, onSelect: () => void change(r, s) }))
    acts.push({ key: 'flags', label: t('housekeeping.flagsButton'), testId: `flags-${r.room_number}`, onSelect: () => startFlags(r) })
  }
  if (canReport.value) acts.push({ key: 'report', label: t('housekeeping.report'), to: `/maintenance?room=${r.room_id}`, testId: `report-${r.room_number}` })
  return acts
}

async function load(keepError = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) {
    rooms.value = []
    return
  }
  loading.value = true
  if (!keepError) error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping', { params: { path: { propertyId } } })
    rooms.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

// Focus goes to the note when the sheet opens, and back to the "..." of the room when it closes (the menu that chose the room has closed by then).
function focusNote(e: Event): void {
  e.preventDefault()
  flagSheet.value?.querySelector<HTMLInputElement>('input[name=note]')?.focus()
}
function focusRoomMenu(e: Event): void {
  e.preventDefault()
  const number = lastFlagged.value
  void nextTick(() => {
    const all = [...document.querySelectorAll<HTMLElement>(`[data-testid="room-${number}"] [data-slot=row-menu-trigger]`)]
    ;(all.find((el) => el.getClientRects().length > 0) ?? all[0])?.focus()
  })
}
function startFlags(room: HousekeepingBoardRoom): void {
  flagging.value = room
  lastFlagged.value = room.room_number
  Object.assign(flagForm, { priority: room.priority, dnd: room.dnd, make_up_requested: room.make_up_requested, note: room.flag_note ?? '' })
  error.value = null
}

async function saveFlags(): Promise<void> {
  const room = flagging.value
  if (property.currentId === null || !room) return
  savingFlags.value = true
  error.value = null
  try {
    await api.PUT('/api/v1/properties/{propertyId}/rooms/{id}/housekeeping/flags', {
      params: { path: { propertyId: property.currentId, id: room.room_id } },
      body: { ...flagForm },
    })
    flagging.value = null
    await load(true)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    savingFlags.value = false
  }
}

async function change(room: HousekeepingBoardRoom, status: HousekeepingStatus): Promise<void> {
  if (property.currentId === null) return
  busyRoom.value = room.room_id
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/rooms/{id}/housekeeping', {
      params: { path: { propertyId: property.currentId, id: room.room_id } },
      body: { status },
    })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busyRoom.value = null
  }
  // Reload on success and on failure (another user may have changed the room), keeping the error visible.
  await load(true)
}

watch(() => property.currentId, () => load(), { immediate: true })
</script>

<template>
  <PageHeader :title="t('housekeeping.title')">
    <template #actions>
      <Button variant="outline" size="sm" :disabled="loading" @click="load()"><RefreshCw :class="loading && 'animate-spin'" />{{ t('common.refresh') }}</Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="hk-error" />
  <p v-if="property.currentId === null" class="muted">{{ t('housekeeping.selectProperty') }}</p>

  <template v-else>
    <FilterBar plain class="mb-4 flex flex-wrap items-end justify-between gap-3" :active="activeFilters">
      <div class="flex flex-wrap gap-2 pointer-coarse:gap-3" role="group" :aria-label="t('housekeeping.filterStatus')">
        <Button size="sm" :variant="statusFilter === '' ? 'default' : 'outline'" :aria-pressed="statusFilter === ''" @click="statusFilter = ''">
          {{ t('housekeeping.all') }} <b>{{ statusPool.length }}</b>
        </Button>
        <Button
          v-for="s in statuses"
          :key="s"
          size="sm"
          :variant="statusFilter === s ? 'default' : 'outline'"
          :aria-pressed="statusFilter === s"
          :data-testid="`filter-${s}`"
          @click="statusFilter = statusFilter === s ? '' : s"
        >
          {{ label(s) }} <b>{{ counts[s] }}</b>
        </Button>
        <Button size="sm" :variant="flaggedOnly ? 'default' : 'outline'" :aria-pressed="flaggedOnly" data-testid="filter-flagged" @click="flaggedOnly = !flaggedOnly">
          {{ t('housekeeping.flagged') }} <b>{{ flaggedCount }}</b>
        </Button>
      </div>
      <div class="flex flex-wrap gap-3">
        <FormField class="w-40" :label="t('housekeeping.occupancy')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="occupancyFilter" name="occupancy">
              <option value="">{{ t('housekeeping.all') }}</option>
              <option value="OCCUPIED">{{ t('status.OCCUPIED') }}</option>
              <option value="RESERVED">{{ t('status.RESERVED') }}</option>
              <option value="VACANT">{{ t('status.VACANT') }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField v-if="floors.length > 1" class="w-40" :label="t('housekeeping.floor')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="floorFilter" name="floor">
              <option value="">{{ t('housekeeping.allFloors') }}</option>
              <option v-for="f in floors" :key="f" :value="f">{{ f }}</option>
            </NativeSelect>
          </template>
        </FormField>
      </div>
    </FilterBar>

    <Sheet :open="!!flagging" @update:open="(v: boolean) => { if (!v) flagging = null }">
      <SheetContent v-if="flagging" class="p-6" data-testid="flag-sheet" @open-auto-focus="focusNote" @close-auto-focus="focusRoomMenu">
        <div ref="flagSheet">
          <SheetTitle class="text-lg">{{ t('housekeeping.flagsTitle', { number: flagging.room_number }) }}</SheetTitle>
          <SheetDescription class="mt-1">{{ t('housekeeping.flagsHelp') }}</SheetDescription>
          <form class="mt-4" novalidate data-testid="flag-form" @submit.prevent="saveFlags">
            <div class="grid gap-4">
              <FormField :label="t('housekeeping.priority')">
                <template #default="{ id }">
                  <NativeSelect :id="id" v-model="flagForm.priority" name="priority">
                    <option value="NORMAL">{{ t('housekeeping.normal') }}</option>
                    <option value="HIGH">{{ t('housekeeping.high') }}</option>
                  </NativeSelect>
                </template>
              </FormField>
              <FormField :label="t('housekeeping.note')">
                <template #default="{ id }"><Input :id="id" v-model="flagForm.note" name="note" maxlength="500" /></template>
              </FormField>
              <Checkbox v-model="flagForm.dnd" name="dnd" label-class="text-sm">{{ t('housekeeping.dnd') }}</Checkbox>
              <Checkbox v-model="flagForm.make_up_requested" name="make_up_requested" label-class="text-sm">{{ t('housekeeping.makeUp') }}</Checkbox>
            </div>
            <div class="mt-4 flex justify-end gap-3">
              <Button type="button" variant="outline" @click="flagging = null">{{ t('common.cancel') }}</Button>
              <Button type="submit" :disabled="savingFlags">{{ t('common.save') }}</Button>
            </div>
          </form>
        </div>
      </SheetContent>
    </Sheet>

    <p class="muted mb-2 mt-0" role="status" aria-live="polite" data-testid="result-count">{{ t('housekeeping.resultCount', { n: visible.length }, visible.length) }}</p>

    <DataTable :columns="columns" :rows="visible" row-key="room_id" :loading="loading && !rooms.length" cards :row-actions="roomActions" :row-test-id="(r) => `room-${r.room_number}`" :caption="t('housekeeping.title')">
      <template #cell-room_number="{ row: r }">
        <b>{{ r.room_number }}</b>
        <Badge v-if="r.bed_type_code" variant="outline" class="ml-1.5" data-testid="bed">{{ r.bed_type_code }}</Badge>
        <small v-if="r.floor || r.building" class="ml-1 text-muted-foreground">{{ [r.building, r.floor && t('housekeeping.floorWord', { floor: r.floor })].filter(Boolean).join(', ') }}</small>
      </template>
      <template #cell-status="{ row: r }"><StatusBadge domain="housekeeping" :status="r.status" :label="label(r.status)" data-testid="status" /></template>
      <template #cell-occupancy="{ row: r }"><StatusBadge domain="occupancy" :status="r.occupancy" /></template>
      <template #cell-notes="{ row: r }">
        <span class="inline-flex flex-wrap items-center gap-1"><!-- always drawn, so that a room with nothing to note shows an empty cell, not a dash -->
          <Badge v-if="r.block" variant="warning" data-testid="block"><Lock />{{ t('housekeeping.blockUntil', { type: r.block.type, date: $date(r.block.end_date) }) }}</Badge>
          <span v-if="hasFlag(r)" data-testid="flags" class="inline-flex flex-wrap items-center gap-1">
            <Badge v-if="r.priority === 'HIGH'" variant="destructive"><ChevronsUp />{{ t('housekeeping.highShort') }}</Badge>
            <Badge v-if="r.dnd" variant="warning"><BellOff />{{ t('housekeeping.dndShort') }}</Badge>
            <Badge v-if="r.make_up_requested" variant="secondary"><Sparkles />{{ t('housekeeping.makeUpShort') }}</Badge>
            <small v-if="r.flag_note" class="inline-flex items-center gap-1 text-muted-foreground"><StickyNote class="size-3.5" />{{ r.flag_note }}</small>
          </span>
        </span>
      </template>
      <template #empty>
        <EmptyState v-if="!rooms.length" :title="t('housekeeping.empty')" :description="t('emptyState.housekeeping')" data-testid="empty" />
        <EmptyState v-else :title="t('housekeeping.noMatch')" :description="t('emptyState.noMatch')" :action-label="t('dataTable.clearFilters')" action-variant="outline" @action="clearFilters" />
      </template>
    </DataTable>
  </template>
</template>
