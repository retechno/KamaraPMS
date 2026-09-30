<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { CancelResult, FreeRoom, Reservation, ReservationRoom, RoomType } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { guestLabel, statusLabel } from '@/utils/reservations'

const props = defineProps<{ id: string }>()
const auth = useAuthStore()
const property = usePropertyStore()

const res = ref<Reservation | null>(null)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const types = ref<RoomType[]>([])
// The action that is waiting for a reason, and the room-assignment picker.
const asking = ref<{ kind: 'cancel' | 'cancel-room' | 'no-show'; lineId?: number } | null>(null)
const reason = ref('')
const assigning = ref<{ lineId: number; typeId: number; rooms: FreeRoom[]; roomId: number | null } | null>(null)
const header = reactive({ source: 'PHONE', remarks: '' })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const businessDate = computed(() => property.clock?.business_date ?? '')
const status = computed(() => res.value?.status ?? '')
const fieldError = (field: string) => error.value?.fieldMessage(field)
const estimateTotal = computed(() => (res.value?.rooms ?? []).filter((r) => r.status !== 'CANCELLED').reduce((sum, r) => sum + Number(r.estimate.total), 0))

function adopt(r: Reservation): void {
  res.value = r
  header.source = r.source
  header.remarks = r.remarks ?? ''
}

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('reservation.read')) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations/{id}', { params: { path: { propertyId, id: Number(props.id) } } })
    if (data) adopt(data)
    types.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

/** Runs one action; the reservation in the answer replaces the one on screen. A version conflict reloads it. */
async function run(action: () => Promise<{ data?: Reservation | CancelResult }>): Promise<void> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await action()
    if (data && 'reservation' in data) {
      adopt(data.reservation)
      if (data.requires_folio_resolution) notice.value = `The folios still hold ${data.folio_balance}: refund the deposit or post a fee.`
    } else if (data) {
      adopt(data)
    }
    asking.value = null
    assigning.value = null
    reason.value = ''
  } catch (e) {
    const failure = e instanceof ApiError ? e : null
    error.value = failure
    if (failure?.code === 'VERSION_CONFLICT') {
      await load() // show the current state, and keep the message that explains why the action did not happen
      error.value = failure
    }
  } finally {
    busy.value = false
  }
}

const base = () => ({ path: { propertyId: pid.value as number, id: Number(props.id) } })
const lineParams = (lineId: number) => ({ path: { ...base().path, lineId } })
const version = () => res.value?.version ?? 0

const confirm = () => run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/confirm', { params: base(), body: { version: version() } }))
const reinstate = () => run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/reinstate', { params: base(), body: { version: version() } }))
const saveHeader = () => run(() => api.PATCH('/api/v1/properties/{propertyId}/reservations/{id}', {
  params: base(), body: { version: version(), source: header.source as Reservation['source'], remarks: header.remarks },
}))
const unassign = (lineId: number) => run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/unassign-room', { params: lineParams(lineId), body: { version: version() } }))

function ask(kind: 'cancel' | 'cancel-room' | 'no-show', lineId?: number): void {
  asking.value = { kind, lineId }
  reason.value = ''
  error.value = null
}

function submitReason(): Promise<void> {
  const a = asking.value
  if (!a) return Promise.resolve()
  const body = { version: version(), reason: reason.value.trim() }
  if (a.kind === 'cancel') return run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/cancel', { params: base(), body }))
  if (a.kind === 'cancel-room') return run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/cancel', { params: lineParams(a.lineId as number), body }))
  return run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/no-show', { params: lineParams(a.lineId as number), body }))
}

async function startAssign(line: ReservationRoom): Promise<void> {
  assigning.value = { lineId: line.id, typeId: line.room_type_id, rooms: [], roomId: null }
  await loadFree()
}

async function loadFree(): Promise<void> {
  const a = assigning.value
  const r = res.value
  const propertyId = pid.value
  if (!a || !r || propertyId === null) return
  const line = r.rooms.find((l) => l.id === a.lineId)
  if (!line) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/rooms', {
      params: { path: { propertyId }, query: { room_type_id: a.typeId, arrival: line.arrival_date < businessDate.value ? businessDate.value : line.arrival_date, departure: line.departure_date } },
    })
    a.rooms = data?.data ?? []
    a.roomId = a.rooms[0]?.room_id ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function submitAssign(): Promise<void> {
  const a = assigning.value
  const line = res.value?.rooms.find((l) => l.id === a?.lineId)
  if (!a || !line || a.roomId === null) return Promise.resolve()
  const upgrade = a.typeId !== line.room_type_id
  return run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/assign-room', {
    params: lineParams(a.lineId), body: { version: version(), room_id: a.roomId as number, upgrade },
  }))
}

const typeCode = (id: number) => types.value.find((t) => t.id === id)?.code ?? String(id)
const activeTypes = computed(() => types.value.filter((t) => t.is_active))
const canNoShow = (l: ReservationRoom) => can('nightaudit.no_show') && l.status === 'CONFIRMED' && l.arrival_date <= businessDate.value

watch(() => [pid.value, props.id], () => void load(), { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Reservation <span v-if="res">{{ res.confirmation_number }}</span></h1>
    <RouterLink to="/reservations">Reservations</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('reservation.read')" class="muted" data-testid="no-access">Your role at this property does not allow viewing reservations.</p>

  <template v-else-if="res">
    <section class="card" data-testid="summary">
      <div class="head">
        <span class="badge" :class="res.display_status.toLowerCase()" data-testid="status">{{ statusLabel(res.display_status) }}</span>
        <span>{{ res.arrival_date }} &rarr; {{ res.departure_date }}</span>
        <span class="muted">version {{ res.version }}</span>
      </div>
      <p>
        Booker:
        <RouterLink v-if="res.guest" :to="`/guests/${res.guest.id}`" data-testid="booker">{{ guestLabel(res.guest) }}</RouterLink>
        <span v-else class="muted" data-testid="no-booker">not set</span>
        · Source {{ res.source }} · Booked on {{ res.reservation_date }}
      </p>
      <p v-if="res.cancellation_reason" class="muted">Cancelled: {{ res.cancellation_reason }}</p>
      <p>Estimated total (active rooms): <strong data-testid="estimate">{{ estimateTotal }}</strong></p>

      <div class="form-actions">
        <button v-if="status === 'DRAFT' && can('reservation.create')" type="button" class="btn-primary" :disabled="busy" data-testid="confirm" @click="confirm">Confirm</button>
        <button v-if="status !== 'CANCELLED' && can('reservation.cancel')" type="button" :disabled="busy" data-testid="cancel" @click="ask('cancel')">Cancel reservation</button>
        <button v-if="status === 'CANCELLED' && can('reservation.reinstate')" type="button" class="btn-primary" :disabled="busy" data-testid="reinstate" @click="reinstate">Reinstate</button>
      </div>

      <form v-if="asking" class="reason" novalidate data-testid="reason-form" @submit.prevent="submitReason">
        <label class="field">
          <span>{{ asking.kind === 'no-show' ? 'Reason (optional)' : 'Reason' }}</span>
          <input v-model="reason" name="reason" maxlength="500" :aria-invalid="!!fieldError('reason')" />
          <small v-if="fieldError('reason')" class="error-text">{{ fieldError('reason') }}</small>
        </label>
        <button type="submit" class="btn-primary" :disabled="busy">{{ asking.kind === 'no-show' ? 'Mark no-show' : 'Cancel' }}</button>
        <button type="button" @click="asking = null">Keep</button>
      </form>
    </section>

    <form v-if="status !== 'CANCELLED' && can('reservation.update')" class="card header-form" novalidate data-testid="header-form" @submit.prevent="saveHeader">
      <h2>Details</h2>
      <div class="form-grid">
        <label class="field">
          <span>Source</span>
          <select v-model="header.source" name="source">
            <option v-for="s in ['WALK_IN', 'PHONE', 'EMAIL', 'WEBSITE', 'OTA', 'AGENT', 'OTHER']" :key="s" :value="s">{{ s }}</option>
          </select>
        </label>
        <label class="field">
          <span>Remarks</span>
          <input v-model="header.remarks" name="remarks" />
        </label>
      </div>
      <div class="form-actions"><button type="submit" :disabled="busy">Save</button></div>
    </form>

    <section v-for="line in res.rooms" :key="line.id" class="card room" :data-testid="`room-${line.id}`">
      <div class="head">
        <strong>{{ line.room_type_code }}</strong>
        <span v-if="line.room_number" :data-testid="`room-number-${line.id}`">room {{ line.room_number }}</span>
        <span v-else class="muted">no room assigned</span>
        <span class="badge" :class="line.status.toLowerCase()" :data-testid="`line-status-${line.id}`">{{ statusLabel(line.status) }}</span>
        <span>{{ line.arrival_date }} &rarr; {{ line.departure_date }} ({{ line.nights }} night{{ line.nights === 1 ? '' : 's' }})</span>
        <span class="muted">{{ line.adult_count }} adult(s), {{ line.child_count }} child(ren) · plan {{ line.rate_plan_code }}</span>
        <span v-if="line.stay_id" class="muted">stay {{ line.stay_id }}</span>
      </div>

      <table class="list" :data-testid="`nights-${line.id}`">
        <thead><tr><th>Night</th><th class="num">Amount</th><th class="num">Grid</th><th /></tr></thead>
        <tbody>
          <tr v-for="n in line.nightly_rates" :key="n.date">
            <td>{{ n.date }}</td>
            <td class="num">{{ n.amount }}</td>
            <td class="num">{{ n.base_rate ?? '—' }}</td>
            <td><small v-if="n.is_override" class="muted">override</small></td>
          </tr>
        </tbody>
        <tfoot>
          <tr><td>Estimate ({{ line.rate_plan_code }})</td><td class="num" :data-testid="`line-estimate-${line.id}`">{{ line.estimate.total }}</td><td colspan="2" class="muted">net {{ line.estimate.net }}, service {{ line.estimate.service }}, tax {{ line.estimate.tax }}</td></tr>
        </tfoot>
      </table>

      <div class="form-actions">
        <button v-if="line.status === 'CONFIRMED' && can('reservation.update') && !line.room_number" type="button" :disabled="busy" :data-testid="`assign-${line.id}`" @click="startAssign(line)">Assign room</button>
        <button v-if="line.status === 'CONFIRMED' && can('reservation.update') && line.room_number" type="button" :disabled="busy" :data-testid="`unassign-${line.id}`" @click="unassign(line.id)">Unassign room</button>
        <button v-if="canNoShow(line)" type="button" :disabled="busy" :data-testid="`no-show-${line.id}`" @click="ask('no-show', line.id)">No-show</button>
        <button v-if="(line.status === 'DRAFT' || line.status === 'CONFIRMED') && can('reservation.cancel') && status !== 'CANCELLED'" type="button" :disabled="busy" :data-testid="`cancel-room-${line.id}`" @click="ask('cancel-room', line.id)">Cancel room</button>
      </div>

      <form v-if="assigning && assigning.lineId === line.id" class="reason" novalidate :data-testid="`assign-form-${line.id}`" @submit.prevent="submitAssign">
        <label class="field">
          <span>Room type</span>
          <select v-model.number="assigning.typeId" name="assign_type" @change="loadFree">
            <option v-for="t in activeTypes" :key="t.id" :value="t.id">{{ t.code }}{{ t.id === line.room_type_id ? ' (booked)' : ' (upgrade)' }}</option>
          </select>
        </label>
        <label class="field">
          <span>Free room</span>
          <select v-model.number="assigning.roomId" name="assign_room" :disabled="!assigning.rooms.length">
            <option v-for="r in assigning.rooms" :key="r.room_id" :value="r.room_id">{{ r.room_number }} · {{ r.housekeeping_status }}</option>
          </select>
          <small v-if="!assigning.rooms.length" class="muted" data-testid="no-free-rooms">No free {{ typeCode(assigning.typeId) }} room for these dates.</small>
        </label>
        <button type="submit" class="btn-primary" :disabled="busy || assigning.roomId === null">Assign</button>
        <button type="button" @click="assigning = null">Close</button>
      </form>
    </section>

    <section v-if="res.folios.length" class="card" data-testid="folios">
      <h2>Folios</h2>
      <ul>
        <li v-for="f in res.folios" :key="f.id">{{ f.folio_number }} · {{ f.status }} · balance {{ f.balance }}</li>
      </ul>
    </section>
  </template>
</template>

<style scoped>
.head {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  align-items: center;
  margin-bottom: 8px;
}
.reason {
  display: flex;
  gap: 12px;
  align-items: flex-end;
  flex-wrap: wrap;
  margin-top: 12px;
}
.list {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.list th,
.list td {
  text-align: left;
  padding: 4px 8px;
  border-bottom: 1px solid var(--border);
}
.num {
  text-align: right !important;
  font-variant-numeric: tabular-nums;
}
.badge {
  border-radius: 999px;
  padding: 2px 10px;
  font-size: 12px;
  background: var(--accent-soft);
}
.badge.cancelled,
.badge.no_show {
  background: #fde8e8;
}
.badge.draft {
  background: #eef0f3;
}
</style>
