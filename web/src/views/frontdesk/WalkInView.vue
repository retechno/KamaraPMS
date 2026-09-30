<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { FreeRoom, Guest, RatePlan, RoomType } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'
import { guestLabel, newIdempotencyKey } from '@/utils/reservations'

const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()

const types = ref<RoomType[]>([])
const plans = ref<RatePlan[]>([])
const rooms = ref<FreeRoom[]>([])
const guestQuery = ref('')
const guestResults = ref<Guest[]>([])
const guest = ref<Guest | null>(null)
const newGuest = reactive({ first_name: '', last_name: '' })
const form = reactive({ typeId: 0, roomId: null as number | null, planId: 0, departure: '', adults: 1, children: 0, override: false, reason: '' })
const busy = ref(false)
const error = ref<ApiError | null>(null)
let key = newIdempotencyKey()

const pid = computed(() => property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')
const allowed = computed(() => auth.can('frontdesk.checkin', pid.value) && auth.can('reservation.create', pid.value))
const requiresInspection = computed(() => property.current?.require_room_inspection_for_checkin ?? false)
const activeTypes = computed(() => types.value.filter((t) => t.is_active))
const activePlans = computed(() => plans.value.filter((p) => p.is_active))
const selected = computed(() => rooms.value.find((r) => r.room_id === form.roomId))
const isReady = (s: string) => (requiresInspection.value ? s === 'INSPECTED' : s === 'CLEAN' || s === 'INSPECTED')
const notReady = computed(() => !!selected.value && !isReady(selected.value.housekeeping_status))
const canOverride = computed(() => auth.can('frontdesk.checkin_unready_room', pid.value))
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function loadBase(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !allowed.value) return
  try {
    const path = { path: { propertyId } }
    ;[types.value, plans.value] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { ...path, query: { limit: 200, cursor } } })),
    ])
    form.typeId ||= activeTypes.value[0]?.id ?? 0
    form.planId ||= activePlans.value[0]?.id ?? 0
    await loadRooms()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function loadRooms(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !form.typeId || !businessDate.value || !form.departure) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/rooms', {
      params: { path: { propertyId }, query: { room_type_id: form.typeId, arrival: businessDate.value, departure: form.departure } },
    })
    rooms.value = data?.data ?? []
    if (!rooms.value.some((r) => r.room_id === form.roomId)) form.roomId = rooms.value[0]?.room_id ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function findGuests(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !guestQuery.value.trim()) return
  try {
    const { data } = await api.GET('/api/v1/guests', { params: { query: { q: guestQuery.value.trim(), property_id: propertyId, limit: 10 } } })
    guestResults.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function submit(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || form.roomId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/walk-ins', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': key } },
      body: {
        guest_id: guest.value?.id, new_guest: guest.value ? undefined : { first_name: newGuest.first_name || undefined, last_name: newGuest.last_name },
        room_id: form.roomId, rate_plan_id: form.planId, departure_date: form.departure, adult_count: form.adults, child_count: form.children,
        override_room_not_ready: form.override, override_reason: form.override ? form.reason : undefined,
      },
    })
    if (data) await router.push(`/stays/${data.stay.id}`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) key = newIdempotencyKey()
  } finally {
    busy.value = false
  }
}

watch(() => property.currentId, () => void loadBase(), { immediate: true })
watch(businessDate, (bd) => {
  if (bd && !form.departure) {
    form.departure = addDays(bd, 1)
    void loadRooms()
  }
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Walk-in</h1>
    <RouterLink to="/arrivals">Arrivals</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!allowed" class="muted" data-testid="no-access">Your role at this property does not allow walk-ins (check-in and reservation create are both needed).</p>

  <form v-else class="card" novalidate data-testid="walkin-form" @submit.prevent="submit">
    <h2>Stay</h2>
    <div class="form-grid">
      <label class="field">
        <span>Departure</span>
        <input v-model="form.departure" name="departure" type="date" :min="businessDate" :aria-invalid="!!fieldError('departure_date')" @change="loadRooms" />
        <small v-if="fieldError('departure_date')" class="error-text">{{ fieldError('departure_date') }}</small>
      </label>
      <label class="field">
        <span>Room type</span>
        <select v-model.number="form.typeId" name="room_type" @change="loadRooms">
          <option v-for="t in activeTypes" :key="t.id" :value="t.id">{{ t.code }} · {{ t.name }}</option>
        </select>
      </label>
      <label class="field">
        <span>Room</span>
        <select v-model.number="form.roomId" name="room" :disabled="!rooms.length">
          <option v-for="r in rooms" :key="r.room_id" :value="r.room_id">{{ r.room_number }} · {{ r.housekeeping_status }}{{ isReady(r.housekeeping_status) ? '' : ' (not ready)' }}</option>
        </select>
        <small v-if="!rooms.length" class="muted" data-testid="no-rooms">No free room of this type for these dates.</small>
        <small v-if="fieldError('room_id')" class="error-text">{{ fieldError('room_id') }}</small>
      </label>
      <label class="field">
        <span>Rate plan</span>
        <select v-model.number="form.planId" name="rate_plan">
          <option v-for="p in activePlans" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option>
        </select>
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

    <div v-if="notReady" class="alert warning" data-testid="not-ready">
      Room {{ selected?.room_number }} is {{ selected?.housekeeping_status }}.
      <template v-if="canOverride">
        <label class="check"><input v-model="form.override" type="checkbox" name="override" /><span>Use it anyway</span></label>
        <label v-if="form.override" class="field">
          <span>Reason</span>
          <input v-model="form.reason" name="override_reason" maxlength="500" />
        </label>
      </template>
    </div>

    <h2>Guest</h2>
    <div class="guest-pick">
      <label class="field grow">
        <span>Find an existing guest</span>
        <input v-model="guestQuery" name="guest_q" type="search" placeholder="Name, email, phone or code" @keydown.enter.prevent="findGuests" />
      </label>
      <button type="button" data-testid="find-guest" @click="findGuests">Find</button>
    </div>
    <ul v-if="guestResults.length" class="picks">
      <li v-for="g in guestResults" :key="g.id"><button type="button" :data-testid="`guest-${g.code}`" @click="guest = g; guestResults = []">{{ g.code }} · {{ guestLabel(g) }}</button></li>
    </ul>
    <p v-if="guest" data-testid="chosen-guest">Guest: <strong>{{ guestLabel(guest) }}</strong> <button type="button" class="link" @click="guest = null">change</button></p>
    <div v-else class="form-grid">
      <label class="field">
        <span>First name</span>
        <input v-model="newGuest.first_name" name="first_name" />
      </label>
      <label class="field">
        <span>Last name</span>
        <input v-model="newGuest.last_name" name="last_name" :aria-invalid="!!fieldError('new_guest.last_name')" />
        <small v-if="fieldError('new_guest.last_name')" class="error-text">{{ fieldError('new_guest.last_name') }}</small>
      </label>
    </div>
    <small v-if="fieldError('guest_id')" class="error-text">{{ fieldError('guest_id') }}</small>

    <div class="form-actions">
      <button type="submit" class="btn-primary" :disabled="busy || form.roomId === null || (notReady && !form.override) || (!guest && !newGuest.last_name.trim())" data-testid="walkin-submit">Check in</button>
    </div>
  </form>
</template>

<style scoped>
.guest-pick {
  display: flex;
  gap: 12px;
  align-items: flex-end;
}
.grow {
  flex: 1;
}
.picks {
  list-style: none;
  padding: 0;
  margin: 8px 0;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.link {
  border: 0;
  background: none;
  color: var(--accent);
  padding: 0;
  text-decoration: underline;
}
</style>
