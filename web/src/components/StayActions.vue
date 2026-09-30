<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { FreeRoom, Guest, RoomType, StayDetail } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'
import { guestLabel } from '@/utils/reservations'

const props = defineProps<{ detail: StayDetail }>()
const emit = defineEmits<{ changed: [message: string] }>()
const auth = useAuthStore()
const property = usePropertyStore()

type Dialog = '' | 'move' | 'departure' | 'guest'
const open = ref<Dialog>('')
const busy = ref(false)
const error = ref<ApiError | null>(null)

const pid = computed(() => property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')
const isOpen = computed(() => props.detail.stay.status === 'OPEN')
const canMove = computed(() => isOpen.value && auth.can('frontdesk.room_move', pid.value))
const canChange = computed(() => isOpen.value && auth.can('reservation.update', pid.value))
const canAddGuest = computed(() => isOpen.value && auth.can('frontdesk.checkin', pid.value))
const canOverride = computed(() => auth.can('frontdesk.checkin_unready_room', pid.value))
const requiresInspection = computed(() => property.current?.require_room_inspection_for_checkin ?? false)
const fieldError = (field: string) => error.value?.fieldMessage(field)

// The target room must be free from the business date to the departure (at least one night).
const from = computed(() => businessDate.value)
const until = computed(() => (props.detail.stay.departure_date > from.value ? props.detail.stay.departure_date : addDays(from.value, 1)))

function show(d: Dialog): void {
  error.value = null
  open.value = d
  if (d === 'move') void loadTypes()
}

function fail(e: unknown): void {
  error.value = e instanceof ApiError ? e : null
}

// Room move.
const types = ref<RoomType[]>([])
const rooms = ref<FreeRoom[]>([])
const move = reactive({ typeId: 0, roomId: null as number | null, reason: '', override: false, overrideReason: '' })
const selected = computed(() => rooms.value.find((r) => r.room_id === move.roomId))
const isReady = (s: string) => (requiresInspection.value ? s === 'INSPECTED' : s === 'CLEAN' || s === 'INSPECTED')
const notReady = computed(() => !!selected.value && !isReady(selected.value.housekeeping_status))

async function loadTypes(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    types.value = (await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))).filter((t) => t.is_active)
    move.typeId ||= types.value[0]?.id ?? 0
    await loadRooms()
  } catch (e) {
    fail(e)
  }
}

async function loadRooms(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !move.typeId) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/rooms', {
      params: { path: { propertyId }, query: { room_type_id: move.typeId, arrival: from.value, departure: until.value } },
    })
    rooms.value = data?.data ?? []
    if (!rooms.value.some((r) => r.room_id === move.roomId)) move.roomId = rooms.value[0]?.room_id ?? null
  } catch (e) {
    fail(e)
  }
}

async function submitMove(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || move.roomId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/stays/{id}/move', {
      params: { path: { propertyId, id: props.detail.stay.id } },
      body: {
        version: props.detail.stay.version, room_id: move.roomId, reason: move.reason,
        override_room_not_ready: move.override, override_reason: move.override ? move.overrideReason : undefined,
      },
    })
    open.value = ''
    emit('changed', `Moved to room ${data?.new_segment.room_number ?? ''}; the old room is now dirty.`)
  } catch (e) {
    fail(e)
    if (e instanceof ApiError && e.code === 'VERSION_CONFLICT') emit('changed', '')
  } finally {
    busy.value = false
  }
}

// Departure.
const departure = ref('')
const departureChanged = computed(() => !!departure.value && departure.value !== props.detail.stay.departure_date)

function showDeparture(): void {
  departure.value = props.detail.stay.departure_date
  show('departure')
}

async function submitDeparture(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/stays/{id}/change-departure', {
      params: { path: { propertyId, id: props.detail.stay.id } }, body: { version: props.detail.stay.version, departure_date: departure.value },
    })
    open.value = ''
    emit('changed', `Departure is now ${departure.value}.`)
  } catch (e) {
    fail(e)
    if (e instanceof ApiError && e.code === 'VERSION_CONFLICT') emit('changed', '')
  } finally {
    busy.value = false
  }
}

// Accompanying guest.
const guestQuery = ref('')
const guestResults = ref<Guest[]>([])

async function findGuests(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !guestQuery.value.trim()) return
  try {
    const { data } = await api.GET('/api/v1/guests', { params: { query: { q: guestQuery.value.trim(), property_id: propertyId, limit: 10 } } })
    guestResults.value = data?.data ?? []
  } catch (e) {
    fail(e)
  }
}

async function addGuest(g: Guest): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/stays/{id}/guests', {
      params: { path: { propertyId, id: props.detail.stay.id } }, body: { guest_id: g.id },
    })
    open.value = ''
    guestResults.value = []
    guestQuery.value = ''
    emit('changed', `${guestLabel(g)} added to the stay.`)
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section v-if="canMove || canChange || canAddGuest" class="card" data-testid="stay-actions">
    <div class="form-actions">
      <button v-if="canMove" type="button" data-testid="open-move" @click="show('move')">Move room</button>
      <button v-if="canChange" type="button" data-testid="open-departure" @click="showDeparture">Extend / shorten</button>
      <button v-if="canAddGuest" type="button" data-testid="open-guest" @click="show('guest')">Add guest</button>
    </div>
    <p v-if="error" class="alert" role="alert" data-testid="action-error">
      {{ error.message }} <code>{{ error.code }}</code>
      <template v-if="error.code === 'ROOM_NOT_AVAILABLE_FOR_EXTENSION'"> Move the guest to another room first.</template>
    </p>

    <form v-if="open === 'move'" novalidate class="dialog" data-testid="move-form" @submit.prevent="submitMove">
      <h2>Move to another room</h2>
      <div class="form-grid">
        <label class="field">
          <span>Room type</span>
          <select v-model.number="move.typeId" name="room_type" @change="loadRooms">
            <option v-for="t in types" :key="t.id" :value="t.id">{{ t.code }} · {{ t.name }}</option>
          </select>
        </label>
        <label class="field">
          <span>Room</span>
          <select v-model.number="move.roomId" name="room" :disabled="!rooms.length">
            <option v-for="r in rooms" :key="r.room_id" :value="r.room_id">{{ r.room_number }} · {{ r.housekeeping_status }}{{ isReady(r.housekeeping_status) ? '' : ' (not ready)' }}</option>
          </select>
          <small v-if="!rooms.length" class="muted" data-testid="no-rooms">No free room of this type until {{ until }}.</small>
          <small v-if="fieldError('room_id')" class="error-text">{{ fieldError('room_id') }}</small>
        </label>
        <label class="field">
          <span>Reason</span>
          <input v-model="move.reason" name="reason" maxlength="500" :aria-invalid="!!fieldError('reason')" />
          <small v-if="fieldError('reason')" class="error-text">{{ fieldError('reason') }}</small>
        </label>
      </div>
      <div v-if="notReady" class="alert warning" data-testid="not-ready">
        Room {{ selected?.room_number }} is {{ selected?.housekeeping_status }}.
        <template v-if="canOverride">
          <label class="check"><input v-model="move.override" type="checkbox" name="override" /><span>Use it anyway</span></label>
          <label v-if="move.override" class="field"><span>Reason</span><input v-model="move.overrideReason" name="override_reason" maxlength="500" /></label>
        </template>
      </div>
      <div class="form-actions">
        <button type="submit" class="btn-primary" :disabled="busy || move.roomId === null || !move.reason.trim()">Move</button>
        <button type="button" @click="open = ''">Cancel</button>
      </div>
    </form>

    <form v-if="open === 'departure'" novalidate class="dialog" data-testid="departure-form" @submit.prevent="submitDeparture">
      <h2>Change the departure</h2>
      <label class="field">
        <span>Departure</span>
        <input v-model="departure" name="departure" type="date" :min="addDays(businessDate, 1)" :aria-invalid="!!fieldError('departure_date')" />
        <small v-if="fieldError('departure_date')" class="error-text">{{ fieldError('departure_date') }}</small>
      </label>
      <p class="muted">Extra nights are priced from the rate grid. Shortening stops at the last night already charged.</p>
      <div class="form-actions">
        <button type="submit" class="btn-primary" :disabled="busy || !departureChanged">Save</button>
        <button type="button" @click="open = ''">Cancel</button>
      </div>
    </form>

    <div v-if="open === 'guest'" class="dialog" data-testid="guest-form">
      <h2>Add an accompanying guest</h2>
      <div class="guest-pick">
        <label class="field grow">
          <span>Find a guest</span>
          <input v-model="guestQuery" name="guest_q" type="search" placeholder="Name, email, phone or code" @keydown.enter.prevent="findGuests" />
        </label>
        <button type="button" data-testid="find-guest" @click="findGuests">Find</button>
        <button type="button" @click="open = ''">Cancel</button>
      </div>
      <ul v-if="guestResults.length">
        <li v-for="g in guestResults" :key="g.id"><button type="button" :disabled="busy" :data-testid="`guest-${g.code}`" @click="addGuest(g)">{{ g.code }} · {{ guestLabel(g) }}</button></li>
      </ul>
    </div>
  </section>
</template>

<style scoped>
.dialog {
  margin-top: 12px;
  border-top: 1px solid var(--border);
  padding-top: 12px;
}
.guest-pick {
  display: flex;
  gap: 12px;
  align-items: flex-end;
  flex-wrap: wrap;
}
</style>
