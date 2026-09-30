<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { StayDetail } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const props = defineProps<{ id: string }>()
const auth = useAuthStore()
const property = usePropertyStore()

const detail = ref<StayDetail | null>(null)
const error = ref<ApiError | null>(null)
const notice = ref('')
const reversing = ref(false)
const reason = ref('')
const busy = ref(false)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const businessDate = computed(() => property.clock?.business_date ?? '')
const canReverse = computed(() => can('frontdesk.reverse_checkin') && detail.value?.stay.status === 'OPEN' && detail.value.stay.arrival_date === businessDate.value && detail.value.segments.length === 1)
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('reservation.read')) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/stays/{id}', { params: { path: { propertyId, id: Number(props.id) } } })
    detail.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function reverse(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !detail.value) return
  busy.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/stays/{id}/reverse-check-in', {
      params: { path: { propertyId, id: Number(props.id) } }, body: { version: detail.value.stay.version, reason: reason.value },
    })
    notice.value = 'Check-in reversed: the room goes back to the reservation and is marked dirty.'
    reversing.value = false
    reason.value = ''
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (error.value?.code === 'VERSION_CONFLICT') await load()
  } finally {
    busy.value = false
  }
}

watch(() => [pid.value, props.id], () => void load(), { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Stay <span v-if="detail">{{ detail.stay.stay_number }}</span></h1>
    <RouterLink to="/in-house">In-house</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('reservation.read')" class="muted" data-testid="no-access">Your role at this property does not allow viewing stays.</p>

  <template v-else-if="detail">
    <section class="card" data-testid="summary">
      <p>
        <span class="badge" data-testid="stay-status">{{ detail.stay.status }}</span>
        {{ detail.guest.first_name }} {{ detail.guest.last_name }} · {{ detail.stay.arrival_date }} &rarr; {{ detail.stay.departure_date }} ·
        {{ detail.stay.adult_count }} adult(s), {{ detail.stay.child_count }} child(ren) · {{ detail.line.room_type_code }}
      </p>
      <p class="muted">
        Reservation <RouterLink :to="`/reservations/${detail.line.reservation_id}`">{{ detail.line.confirmation_number }}</RouterLink>
        <template v-for="f in detail.folios" :key="f.id"> · Folio <RouterLink :to="`/folios/${f.id}`" :data-testid="`folio-${f.id}`">{{ f.folio_number }}</RouterLink> (balance {{ f.balance }})</template>
      </p>
      <p v-if="detail.guests.length" class="muted" data-testid="companions">With {{ detail.guests.map((g) => `${g.first_name ?? ''} ${g.last_name}`.trim()).join(', ') }}</p>
      <div v-if="canReverse" class="form-actions">
        <button v-if="!reversing" type="button" data-testid="reverse" @click="reversing = true">Reverse check-in</button>
      </div>
      <form v-if="reversing" class="reason" novalidate data-testid="reverse-form" @submit.prevent="reverse">
        <label class="field">
          <span>Reason</span>
          <input v-model="reason" name="reason" maxlength="500" :aria-invalid="!!fieldError('reason')" />
          <small v-if="fieldError('reason')" class="error-text">{{ fieldError('reason') }}</small>
        </label>
        <button type="submit" class="btn-primary" :disabled="busy || !reason.trim()">Reverse</button>
        <button type="button" @click="reversing = false">Keep</button>
      </form>
    </section>

    <section class="card">
      <h2>Rooms</h2>
      <ul data-testid="segments">
        <li v-for="s in detail.segments" :key="s.id">Room {{ s.room_number }} from {{ s.start_business_date }}<template v-if="s.end_business_date"> to {{ s.end_business_date }}</template><template v-else> (current)</template></li>
      </ul>
    </section>

    <section class="card">
      <h2>Nights</h2>
      <table class="list" data-testid="nights">
        <thead><tr><th>Night</th><th class="num">Amount</th><th>Charged</th></tr></thead>
        <tbody>
          <tr v-for="n in detail.nightly_rates" :key="n.date"><td>{{ n.date }}</td><td class="num">{{ n.amount }}</td><td>{{ n.posted ? 'yes' : 'not yet' }}</td></tr>
        </tbody>
      </table>
    </section>
  </template>
</template>

<style scoped>
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
  font-size: 14px;
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
</style>
