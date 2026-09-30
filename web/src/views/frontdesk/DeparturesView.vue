<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { StaySummary } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const rows = ref<StaySummary[]>([])
const nextCursor = ref<string | undefined>()
const error = ref<ApiError | null>(null)
const loading = ref(false)
const loaded = ref(false)

const businessDate = computed(() => property.clock?.business_date ?? '')
const canRead = computed(() => auth.can('reservation.read', property.currentId))
const canCheckOut = computed(() => auth.can('frontdesk.checkout', property.currentId))
const overdue = (s: StaySummary) => s.departure_date < businessDate.value

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value || !businessDate.value) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/stays', {
      params: { path: { propertyId }, query: { status: 'OPEN', departure_until: businessDate.value, limit: 50, cursor: more ? nextCursor.value : undefined } },
    })
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
    loaded.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

watch(() => [property.currentId, businessDate.value], () => {
  rows.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Departures <span v-if="businessDate" class="muted">{{ businessDate }}</span></h1>
    <RouterLink to="/in-house">In-house</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property does not allow viewing stays.</p>

  <section v-else class="card">
    <p v-if="loaded && !rows.length" class="muted" data-testid="empty">No departures are due.</p>
    <table v-else-if="rows.length" class="list">
      <thead><tr><th>Room</th><th>Guest</th><th>Stay</th><th>Arrival</th><th>Departure</th><th /></tr></thead>
      <tbody>
        <tr v-for="s in rows" :key="s.id" :data-testid="`stay-${s.stay_number}`">
          <td>{{ s.room_number }}</td>
          <td>{{ s.guest_name }}</td>
          <td><RouterLink :to="`/stays/${s.id}`">{{ s.stay_number }}</RouterLink></td>
          <td>{{ s.arrival_date }}</td>
          <td>{{ s.departure_date }} <span v-if="overdue(s)" class="badge warn" data-testid="overdue">overdue</span></td>
          <td><RouterLink v-if="canCheckOut" :to="`/stays/${s.id}`" :data-testid="`checkout-${s.stay_number}`">Check out</RouterLink></td>
        </tr>
      </tbody>
    </table>
    <div v-if="nextCursor" class="form-actions">
      <button type="button" :disabled="loading" data-testid="more" @click="load(true)">Load more</button>
    </div>
  </section>
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
.badge {
  border-radius: 999px;
  padding: 1px 8px;
  font-size: 12px;
  background: var(--accent-soft);
}
</style>
