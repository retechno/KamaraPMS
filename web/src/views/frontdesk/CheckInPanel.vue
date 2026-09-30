<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Arrival, CheckInResult, FreeRoom, RoomType } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { newIdempotencyKey } from '@/utils/reservations'

/** The check-in wizard for one arrival: pick a free room (with its housekeeping status), confirm the party, go. */
const props = defineProps<{ arrival: Arrival }>()
const emit = defineEmits<{ done: [result: CheckInResult]; cancel: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()

const types = ref<RoomType[]>([])
const typeId = ref(props.arrival.room_type_id)
const rooms = ref<FreeRoom[]>([])
const roomId = ref<number | null>(props.arrival.room_id ?? null)
const form = reactive({ adults: props.arrival.adult_count, children: props.arrival.child_count, override: false, reason: '' })
const busy = ref(false)
const error = ref<ApiError | null>(null)
let key = newIdempotencyKey() // kept while a request may have been lost, renewed once the server has answered

const pid = computed(() => property.currentId)
const requiresInspection = computed(() => property.current?.require_room_inspection_for_checkin ?? false)
const activeTypes = computed(() => types.value.filter((t) => t.is_active))
const selected = computed(() => rooms.value.find((r) => r.room_id === roomId.value))
const isReady = (status: string) => (requiresInspection.value ? status === 'INSPECTED' : status === 'CLEAN' || status === 'INSPECTED')
const notReady = computed(() => !!selected.value && !isReady(selected.value.housekeeping_status))
const canOverride = computed(() => auth.can('frontdesk.checkin_unready_room', pid.value))
const isUpgrade = computed(() => typeId.value !== props.arrival.room_type_id)
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function loadRooms(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/rooms', {
      params: { path: { propertyId }, query: { room_type_id: typeId.value, arrival: props.arrival.arrival_date, departure: props.arrival.departure_date } },
    })
    const free = data?.data ?? []
    // A room already assigned to this line is not "free" (the line holds it), so it is offered explicitly.
    if (props.arrival.room_id && typeId.value === props.arrival.room_type_id && !free.some((r) => r.room_id === props.arrival.room_id)) {
      free.unshift({ room_id: props.arrival.room_id, room_number: props.arrival.room_number ?? String(props.arrival.room_id), housekeeping_status: 'DIRTY' })
    }
    rooms.value = free
    if (!free.some((r) => r.room_id === roomId.value)) roomId.value = free[0]?.room_id ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

onMounted(async () => {
  const propertyId = pid.value
  if (propertyId !== null) {
    try {
      types.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    }
  }
  await loadRooms()
})

async function submit(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || roomId.value === null || props.arrival.guest_id === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/check-in', {
      params: { path: { propertyId, id: props.arrival.reservation_id, lineId: props.arrival.reservation_room_id }, header: { 'Idempotency-Key': key } },
      body: {
        version: props.arrival.reservation_version, room_id: roomId.value, guest_id: props.arrival.guest_id, adult_count: form.adults, child_count: form.children,
        override_room_not_ready: form.override, override_reason: form.override ? form.reason : undefined,
      },
    })
    if (data) emit('done', data)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) key = newIdempotencyKey()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="card panel" novalidate :data-testid="`checkin-${arrival.reservation_room_id}`" @submit.prevent="submit">
    <h2>Check in {{ arrival.guest_name || arrival.confirmation_number }} · {{ arrival.room_type_code }}</h2>
    <p v-if="error" class="alert" role="alert" data-testid="checkin-error">{{ error.message }} <code>{{ error.code }}</code></p>
    <p v-if="arrival.guest_id === null" class="alert" data-testid="no-guest">The reservation has no guest yet: open it and set the booker first.</p>

    <div class="form-grid">
      <label class="field">
        <span>Room type</span>
        <select v-model.number="typeId" name="room_type" @change="loadRooms">
          <option v-for="t in activeTypes" :key="t.id" :value="t.id">{{ t.code }}{{ t.id === arrival.room_type_id ? ' (booked)' : ' (upgrade)' }}</option>
        </select>
      </label>
      <label class="field">
        <span>Room</span>
        <select v-model.number="roomId" name="room" :disabled="!rooms.length">
          <option v-for="r in rooms" :key="r.room_id" :value="r.room_id">{{ r.room_number }} · {{ r.housekeeping_status }}{{ isReady(r.housekeeping_status) ? '' : ' (not ready)' }}</option>
        </select>
        <small v-if="!rooms.length" class="muted" data-testid="no-rooms">No free room of this type for the stay.</small>
        <small v-if="fieldError('room_id')" class="error-text">{{ fieldError('room_id') }}</small>
      </label>
      <label class="field">
        <span>Adults</span>
        <input v-model.number="form.adults" name="adults" type="number" min="1" :aria-invalid="!!fieldError('adult_count')" />
        <small v-if="fieldError('adult_count')" class="error-text">{{ fieldError('adult_count') }}</small>
      </label>
      <label class="field">
        <span>Children</span>
        <input v-model.number="form.children" name="children" type="number" min="0" />
      </label>
    </div>
    <p v-if="isUpgrade" class="muted" data-testid="upgrade-note">A room of another type is an upgrade and needs the upgrade permission.</p>

    <div v-if="notReady" class="alert warning" data-testid="not-ready">
      Room {{ selected?.room_number }} is {{ selected?.housekeeping_status }}; {{ requiresInspection ? 'an inspected' : 'a clean or inspected' }} room is needed.
      <template v-if="canOverride">
        <label class="check">
          <input v-model="form.override" type="checkbox" name="override" />
          <span>Check in anyway</span>
        </label>
        <label v-if="form.override" class="field">
          <span>Reason</span>
          <input v-model="form.reason" name="override_reason" maxlength="500" :aria-invalid="!!fieldError('override_reason')" />
          <small v-if="fieldError('override_reason')" class="error-text">{{ fieldError('override_reason') }}</small>
        </label>
      </template>
    </div>

    <div class="form-actions">
      <button type="button" @click="emit('cancel')">Cancel</button>
      <button type="submit" class="btn-primary" :disabled="busy || roomId === null || arrival.guest_id === null || (notReady && !form.override)" data-testid="checkin-submit">Check in</button>
    </div>
  </form>
</template>
