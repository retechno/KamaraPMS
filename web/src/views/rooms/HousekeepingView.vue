<script setup lang="ts">
import { BellOff, ChevronsUp, RefreshCw, Sparkles, Wrench } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
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
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const rooms = ref<HousekeepingBoardRoom[]>([])
const loading = ref(false)
const error = ref<ApiError | null>(null)
const busyRoom = ref<number | null>(null)
const statusFilter = ref<HousekeepingStatus | ''>('')
const floorFilter = ref('')
const occupancyFilter = ref<'' | 'OCCUPIED' | 'RESERVED' | 'VACANT'>('')
const flaggedOnly = ref(false)
// The room whose flags are being edited.
const flagging = ref<HousekeepingBoardRoom | null>(null)
const flagForm = reactive({ priority: 'NORMAL' as 'NORMAL' | 'HIGH', dnd: false, make_up_requested: false, note: '' })
const savingFlags = ref(false)

const statuses: HousekeepingStatus[] = ['DIRTY', 'CLEANING', 'CLEAN', 'INSPECTED']
const label = (s: HousekeepingStatus): string => t(`housekeeping.${s.toLowerCase()}` as never)
const verb = (s: HousekeepingStatus): string => t(({ DIRTY: 'housekeeping.markDirty', CLEANING: 'housekeeping.startCleaning', CLEAN: 'housekeeping.markClean', INSPECTED: 'housekeeping.inspect' } as const)[s])

const canUpdate = computed(() => auth.can('housekeeping.update', property.currentId))
const canInspect = computed(() => auth.can('housekeeping.inspect', property.currentId))
const canReport = computed(() => auth.can('maintenance.report', property.currentId))

// The board is small by nature (one row per active room), so it is loaded whole and filtered here.
const floors = computed(() => [...new Set(rooms.value.map((r) => r.floor ?? '').filter(Boolean))].sort())
const counts = computed(() => Object.fromEntries(statuses.map((s) => [s, rooms.value.filter((r) => r.status === s).length])))
const hasFlag = (r: HousekeepingBoardRoom) => r.priority === 'HIGH' || r.dnd || r.make_up_requested || !!r.flag_note
const flaggedCount = computed(() => rooms.value.filter(hasFlag).length)
const visible = computed(() =>
  rooms.value.filter(
    (r) =>
      (!statusFilter.value || r.status === statusFilter.value) &&
      (!floorFilter.value || r.floor === floorFilter.value) &&
      (!occupancyFilter.value || r.occupancy === occupancyFilter.value) &&
      (!flaggedOnly.value || hasFlag(r)),
  ),
)

const columns = computed<Column<HousekeepingBoardRoom>[]>(() => [
  { key: 'room_number', label: t('housekeeping.room'), sortable: true },
  { key: 'room_type_code', label: t('housekeeping.type'), sortable: true },
  { key: 'status', label: t('housekeeping.housekeepingCol'), sortable: true },
  { key: 'occupancy', label: t('housekeeping.occupancyCol'), sortable: true },
  { key: 'block', label: t('housekeeping.block') },
  { key: 'flags', label: t('housekeeping.flags') },
  ...(canUpdate.value || canReport.value ? [{ key: 'actions', label: t('housekeeping.actions'), align: 'right' as const }] : []),
])

function actionsFor(room: HousekeepingBoardRoom): HousekeepingStatus[] {
  return room.allowed_next.filter((s) => s !== 'INSPECTED' || canInspect.value)
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

function startFlags(room: HousekeepingBoardRoom): void {
  flagging.value = room
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

  <p v-if="error" class="alert" role="alert" data-testid="hk-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('housekeeping.selectProperty') }}</p>

  <template v-else>
    <div class="mb-4 flex flex-wrap items-end justify-between gap-3">
      <div class="flex flex-wrap gap-2" role="group" :aria-label="t('housekeeping.filterStatus')">
        <Button size="sm" :variant="statusFilter === '' ? 'default' : 'outline'" :aria-pressed="statusFilter === ''" @click="statusFilter = ''">
          {{ t('housekeeping.all') }} <b>{{ rooms.length }}</b>
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
    </div>

    <Card v-if="flagging" class="mb-4 border-primary/50">
      <form novalidate data-testid="flag-form" @submit.prevent="saveFlags">
        <CardHeader><CardTitle>{{ t('housekeeping.flagsTitle', { number: flagging.room_number }) }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2">
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
            <label class="flex items-center gap-2 text-sm"><input v-model="flagForm.dnd" name="dnd" type="checkbox" /><span>{{ t('housekeeping.dnd') }}</span></label>
            <label class="flex items-center gap-2 text-sm"><input v-model="flagForm.make_up_requested" name="make_up_requested" type="checkbox" /><span>{{ t('housekeeping.makeUp') }}</span></label>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="flagging = null">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="savingFlags">{{ t('common.save') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <DataTable :columns="columns" :rows="visible" row-key="room_id" :loading="loading && !rooms.length" :row-test-id="(r) => `room-${r.room_number}`" :caption="t('housekeeping.title')">
      <template #cell-room_number="{ row: r }">
        <b>{{ r.room_number }}</b>
        <small v-if="r.floor || r.building" class="ml-1 text-muted-foreground">{{ [r.building, r.floor && t('housekeeping.floorWord', { floor: r.floor })].filter(Boolean).join(', ') }}</small>
      </template>
      <template #cell-status="{ row: r }"><StatusBadge domain="housekeeping" :status="r.status" :label="label(r.status)" data-testid="status" /></template>
      <template #cell-occupancy="{ row: r }"><StatusBadge domain="occupancy" :status="r.occupancy" /></template>
      <template #cell-block="{ row: r }">
        <Badge v-if="r.block" variant="warning" data-testid="block">{{ t('housekeeping.blockUntil', { type: r.block.type, date: $date(r.block.end_date) }) }}</Badge>
      </template>
      <template #cell-flags="{ row: r }">
        <span data-testid="flags" class="inline-flex flex-wrap items-center gap-1">
          <Badge v-if="r.priority === 'HIGH'" variant="destructive"><ChevronsUp />{{ t('housekeeping.highShort') }}</Badge>
          <Badge v-if="r.dnd" variant="warning"><BellOff />{{ t('housekeeping.dndShort') }}</Badge>
          <Badge v-if="r.make_up_requested" variant="secondary"><Sparkles />{{ t('housekeeping.makeUpShort') }}</Badge>
          <small v-if="r.flag_note" class="text-muted-foreground">{{ r.flag_note }}</small>
        </span>
      </template>
      <template #cell-actions="{ row: r }">
        <span class="inline-flex flex-wrap justify-end gap-1.5">
          <Button v-if="canReport" as-child variant="ghost" size="sm" :data-testid="`report-${r.room_number}`">
            <RouterLink :to="{ path: '/maintenance', query: { room: r.room_id } }"><Wrench />{{ t('housekeeping.report') }}</RouterLink>
          </Button>
          <Button v-if="canUpdate" variant="outline" size="sm" :data-testid="`flags-${r.room_number}`" @click="startFlags(r)">{{ t('housekeeping.flagsButton') }}</Button>
          <Button
            v-for="s in (canUpdate ? actionsFor(r) : [])"
            :key="s"
            variant="outline"
            size="sm"
            :disabled="busyRoom === r.room_id"
            :data-testid="`act-${s}`"
            @click="change(r, s)"
          >
            {{ verb(s) }}
          </Button>
        </span>
      </template>
      <template #empty>
        <EmptyState v-if="!rooms.length" :title="t('housekeeping.empty')" data-testid="empty" />
        <EmptyState v-else :title="t('housekeeping.noMatch')" />
      </template>
    </DataTable>
  </template>
</template>
