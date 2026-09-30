<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Arrival, CheckInResult } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CheckInPanel from './CheckInPanel.vue'

const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()

const rows = ref<Arrival[]>([])
const open = ref<number | null>(null)
const error = ref<ApiError | null>(null)
const loaded = ref(false)

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const canCheckIn = computed(() => auth.can('frontdesk.checkin', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')

async function load(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/arrivals', { params: { path: { propertyId }, query: {} } })
    rows.value = data?.data ?? []
    loaded.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function checkedIn(result: CheckInResult): Promise<void> {
  open.value = null
  await router.push(`/stays/${result.stay.id}`)
}

watch(() => property.currentId, () => {
  rows.value = []
  loaded.value = false
  open.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Arrivals <span class="muted">{{ businessDate }}</span></h1>
    <RouterLink v-if="canCheckIn" to="/walk-in" class="btn-primary" data-testid="walk-in">Walk-in</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property does not allow viewing arrivals.</p>

  <template v-else>
    <section class="card">
      <p v-if="loaded && !rows.length" class="muted" data-testid="empty">No one is due to arrive.</p>
      <table v-else-if="rows.length" class="list">
        <thead><tr><th>Confirmation</th><th>Guest</th><th>Room type</th><th>Room</th><th>Departure</th><th>Party</th><th /></tr></thead>
        <tbody>
          <template v-for="a in rows" :key="a.reservation_room_id">
            <tr :data-testid="`arrival-${a.reservation_room_id}`">
              <td><RouterLink :to="`/reservations/${a.reservation_id}`">{{ a.confirmation_number }}</RouterLink></td>
              <td>{{ a.guest_name || '—' }}</td>
              <td>{{ a.room_type_code }}</td>
              <td>{{ a.room_number || '—' }}</td>
              <td>{{ a.departure_date }}</td>
              <td>{{ a.adult_count }}+{{ a.child_count }}</td>
              <td><button v-if="canCheckIn" type="button" :data-testid="`open-${a.reservation_room_id}`" @click="open = open === a.reservation_room_id ? null : a.reservation_room_id">Check in</button></td>
            </tr>
            <tr v-if="open === a.reservation_room_id" class="panel-row">
              <td colspan="7"><CheckInPanel :arrival="a" @done="checkedIn" @cancel="open = null" /></td>
            </tr>
          </template>
        </tbody>
      </table>
    </section>
  </template>
</template>

<style scoped>
.list {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
}
.list th,
.list td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--border);
}
.panel-row td {
  padding: 0;
  border: 0;
}
.btn-primary {
  padding: 6px 14px;
  border-radius: 8px;
  text-decoration: none;
}
</style>
