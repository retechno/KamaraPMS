<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { HousekeepingStatus, Room, RoomType } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const rooms = ref<Room[]>([])
const types = ref<RoomType[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<Room | 'new' | null>(null)
const typeFilter = ref('')

const canManage = computed(() => auth.can('room.manage', property.currentId))
const typeById = computed(() => new Map(types.value.map((t) => [t.id, t])))
const assignable = computed(() => types.value.filter((t) => t.is_active || (editing.value !== 'new' && editing.value?.room_type_id === t.id)))
const visible = computed(() => {
  const list = typeFilter.value ? rooms.value.filter((r) => r.room_type_id === Number(typeFilter.value)) : rooms.value
  return [...list].sort((a, b) => a.room_number.localeCompare(b.room_number, undefined, { numeric: true }))
})

const blank = () => ({
  room_number: '',
  room_type_id: 0,
  floor: '',
  building: '',
  is_active: true,
  initial_housekeeping_status: 'DIRTY' as HousekeepingStatus,
})
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

/** Stays and reservations that block a type change or deactivation (from the ROOM_IN_USE problem). */
const conflicts = computed(() => {
  const c = error.value?.context.conflicts
  return Array.isArray(c) ? (c as { type: string; id: number; reference?: string; from: string; to: string }[]) : []
})

async function load(): Promise<void> {
  const propertyId = property.currentId
  rooms.value = []
  types.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    const [t, r] = await Promise.all([
      fetchAll((cursor) =>
        api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }),
      ),
      fetchAll((cursor) =>
        api.GET('/api/v1/properties/{propertyId}/rooms', { params: { path: { propertyId }, query: { limit: 200, cursor } } }),
      ),
    ])
    types.value = t
    rooms.value = r
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, blank(), { room_type_id: types.value.find((t) => t.is_active)?.id ?? 0 })
  error.value = null
  editing.value = 'new'
}

function startEdit(r: Room): void {
  Object.assign(form, blank(), { ...r, floor: r.floor ?? '', building: r.building ?? '' })
  error.value = null
  editing.value = r
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/rooms', {
        params: { path: { propertyId } },
        body: {
          room_number: form.room_number,
          room_type_id: Number(form.room_type_id),
          floor: form.floor || undefined,
          building: form.building || undefined,
          is_active: form.is_active,
          initial_housekeeping_status: form.initial_housekeeping_status,
        },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/rooms/{id}', {
        params: { path: { propertyId, id: editing.value.id } },
        body: {
          room_number: form.room_number,
          room_type_id: Number(form.room_type_id),
          floor: form.floor,
          building: form.building,
          is_active: form.is_active,
        },
      })
    }
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => property.currentId, load, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Rooms</h1>
    <button v-if="canManage && !editing" type="button" class="btn-primary" :disabled="!types.length" @click="startNew">New room</button>
  </div>

  <div v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <ul v-if="conflicts.length" class="conflicts" data-testid="conflicts">
      <li v-for="c in conflicts" :key="`${c.type}-${c.id}`">
        {{ c.type === 'STAY' ? `In-house stay ${c.reference ?? c.id}` : `Reservation line ${c.id}` }}: {{ c.from }} to {{ c.to }}
      </li>
    </ul>
  </div>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="loaded && !types.length" class="muted">Create a room type before adding rooms.</p>

  <form v-if="editing" class="card" novalidate @submit.prevent="save">
    <h2>{{ editing === 'new' ? 'New room' : `Edit room ${editing.room_number}` }}</h2>
    <div class="form-grid">
      <label class="field">
        <span>Room number</span>
        <input v-model="form.room_number" name="room_number" :aria-invalid="!!fieldError('room_number')" />
        <small v-if="fieldError('room_number')" class="error-text">{{ fieldError('room_number') }}</small>
      </label>
      <label class="field">
        <span>Room type</span>
        <select v-model="form.room_type_id" name="room_type_id" :aria-invalid="!!fieldError('room_type_id')">
          <option v-for="t in assignable" :key="t.id" :value="t.id">{{ t.code }} · {{ t.name }}</option>
        </select>
        <small v-if="fieldError('room_type_id')" class="error-text">{{ fieldError('room_type_id') }}</small>
      </label>
      <label class="field">
        <span>Floor</span>
        <input v-model="form.floor" name="floor" />
      </label>
      <label class="field">
        <span>Building</span>
        <input v-model="form.building" name="building" />
      </label>
      <label v-if="editing === 'new'" class="field">
        <span>Initial housekeeping</span>
        <select v-model="form.initial_housekeeping_status" name="initial_housekeeping_status">
          <option value="DIRTY">Dirty</option>
          <option value="CLEAN">Clean</option>
          <option value="INSPECTED">Inspected</option>
        </select>
      </label>
      <label class="check">
        <input v-model="form.is_active" name="is_active" type="checkbox" />
        <span>Active (in service)</span>
      </label>
    </div>
    <div class="form-actions">
      <button type="button" @click="editing = null">Cancel</button>
      <button type="submit" class="btn-primary" :disabled="saving">Save</button>
    </div>
  </form>

  <section class="card">
    <label v-if="types.length > 1" class="field filter">
      <span>Room type</span>
      <select v-model="typeFilter" name="type_filter">
        <option value="">All types</option>
        <option v-for="t in types" :key="t.id" :value="t.id">{{ t.code }}</option>
      </select>
    </label>
    <p v-if="loaded && !rooms.length && types.length" class="muted" data-testid="empty">No rooms yet.</p>
    <table v-else-if="rooms.length" class="list">
      <thead>
        <tr>
          <th>Room</th>
          <th>Type</th>
          <th>Floor</th>
          <th>Building</th>
          <th>Status</th>
          <th v-if="canManage" />
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in visible" :key="r.id" :data-testid="`room-${r.room_number}`">
          <td>
            <b>{{ r.room_number }}</b>
          </td>
          <td>{{ typeById.get(r.room_type_id)?.code }}</td>
          <td>{{ r.floor }}</td>
          <td>{{ r.building }}</td>
          <td>{{ r.is_active ? 'Active' : 'Inactive' }}</td>
          <td v-if="canManage"><button type="button" @click="startEdit(r)">Edit</button></td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<style scoped>
.filter {
  max-width: 220px;
  margin-bottom: 12px;
}
.conflicts {
  margin: 6px 0 0;
  padding-left: 18px;
}
</style>
