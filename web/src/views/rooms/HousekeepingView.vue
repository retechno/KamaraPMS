<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom, HousekeepingStatus } from '@/api/types'
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

const statuses: HousekeepingStatus[] = ['DIRTY', 'CLEANING', 'CLEAN', 'INSPECTED']
const labels: Record<HousekeepingStatus, string> = { DIRTY: 'Dirty', CLEANING: 'Cleaning', CLEAN: 'Clean', INSPECTED: 'Inspected' }
const verbs: Record<HousekeepingStatus, string> = {
  DIRTY: 'Mark dirty',
  CLEANING: 'Start cleaning',
  CLEAN: 'Mark clean',
  INSPECTED: 'Inspect',
}

const canUpdate = computed(() => auth.can('housekeeping.update', property.currentId))
const canInspect = computed(() => auth.can('housekeeping.inspect', property.currentId))

// The board is small by nature (one row per active room), so it is loaded whole and filtered here.
const floors = computed(() => [...new Set(rooms.value.map((r) => r.floor ?? '').filter(Boolean))].sort())
const counts = computed(() => Object.fromEntries(statuses.map((s) => [s, rooms.value.filter((r) => r.status === s).length])))
const visible = computed(() =>
  rooms.value.filter((r) => (!statusFilter.value || r.status === statusFilter.value) && (!floorFilter.value || r.floor === floorFilter.value)),
)

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
  <div class="page-head">
    <h1 class="page-title">Housekeeping</h1>
    <button type="button" :disabled="loading" @click="load()">Refresh</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="hk-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property to see its rooms.</p>

  <template v-else>
    <div class="toolbar">
      <div class="chips" role="group" aria-label="Filter by status">
        <button type="button" class="chip" :class="{ on: statusFilter === '' }" @click="statusFilter = ''">
          All <b>{{ rooms.length }}</b>
        </button>
        <button
          v-for="s in statuses"
          :key="s"
          type="button"
          class="chip"
          :class="[`st-${s.toLowerCase()}`, { on: statusFilter === s }]"
          :data-testid="`filter-${s}`"
          @click="statusFilter = statusFilter === s ? '' : s"
        >
          {{ labels[s] }} <b>{{ counts[s] }}</b>
        </button>
      </div>
      <label v-if="floors.length > 1" class="field floor">
        <span>Floor</span>
        <select v-model="floorFilter" name="floor">
          <option value="">All floors</option>
          <option v-for="f in floors" :key="f" :value="f">{{ f }}</option>
        </select>
      </label>
    </div>

    <section class="card">
      <p v-if="!loading && !rooms.length" class="muted" data-testid="empty">No active rooms yet. Add rooms under Setup.</p>
      <p v-else-if="!visible.length" class="muted">No rooms match the filter.</p>
      <table v-else class="list">
        <thead>
          <tr>
            <th>Room</th>
            <th>Type</th>
            <th>Housekeeping</th>
            <th>Occupancy</th>
            <th>Block</th>
            <th v-if="canUpdate"><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in visible" :key="r.room_id" :data-testid="`room-${r.room_number}`">
            <td>
              <b>{{ r.room_number }}</b>
              <small v-if="r.floor || r.building" class="muted"> {{ [r.building, r.floor && `floor ${r.floor}`].filter(Boolean).join(', ') }}</small>
            </td>
            <td>{{ r.room_type_code }}</td>
            <td>
              <span class="pill" :class="`st-${r.status.toLowerCase()}`" data-testid="status">{{ labels[r.status] }}</span>
            </td>
            <td>
              <span class="occ" :class="`occ-${r.occupancy.toLowerCase()}`">{{ r.occupancy.toLowerCase() }}</span>
            </td>
            <td>
              <span v-if="r.block" class="pill blocked" data-testid="block">{{ r.block.type }} until {{ r.block.end_date }}</span>
            </td>
            <td v-if="canUpdate" class="actions">
              <button
                v-for="s in actionsFor(r)"
                :key="s"
                type="button"
                :disabled="busyRoom === r.room_id"
                :data-testid="`act-${s}`"
                @click="change(r, s)"
              >
                {{ verbs[s] }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </template>
</template>

<style scoped>
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}
.chips {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.chip b {
  margin-left: 4px;
}
.chip.on {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent-strong);
}
.floor {
  min-width: 140px;
}
.pill {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 999px;
  border: 1px solid var(--border);
  font-size: 12px;
  font-weight: 600;
}
.st-dirty {
  border-color: var(--danger);
}
.st-cleaning {
  border-color: var(--warning);
}
.st-clean {
  border-color: var(--accent);
}
.st-inspected {
  border-color: var(--accent-strong);
  background: var(--accent-soft);
}
.pill.blocked {
  border-color: var(--warning);
  background: color-mix(in srgb, var(--warning) 14%, transparent);
}
.occ {
  font-size: 13px;
  text-transform: capitalize;
}
.occ-occupied {
  font-weight: 600;
}
.occ-vacant {
  color: var(--text-muted);
}
.actions {
  display: flex;
  gap: 6px;
  justify-content: flex-end;
  flex-wrap: wrap;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
}
</style>
