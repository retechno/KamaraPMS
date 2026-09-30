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

const canRead = computed(() => auth.can('reservation.read', property.currentId))

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/stays', {
      params: { path: { propertyId }, query: { status: 'OPEN', limit: 50, cursor: more ? nextCursor.value : undefined } },
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

watch(() => property.currentId, () => {
  rows.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">In-house</h1>
    <RouterLink to="/arrivals">Arrivals</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property does not allow viewing stays.</p>

  <section v-else class="card">
    <p v-if="loaded && !rows.length" class="muted" data-testid="empty">Nobody is in house.</p>
    <table v-else-if="rows.length" class="list">
      <thead><tr><th>Room</th><th>Guest</th><th>Stay</th><th>Arrival</th><th>Departure</th><th>Party</th></tr></thead>
      <tbody>
        <tr v-for="s in rows" :key="s.id" :data-testid="`stay-${s.stay_number}`">
          <td>{{ s.room_number }}</td>
          <td>{{ s.guest_name }}</td>
          <td><RouterLink :to="`/stays/${s.id}`">{{ s.stay_number }}</RouterLink></td>
          <td>{{ s.arrival_date }}</td>
          <td>{{ s.departure_date }}</td>
          <td>{{ s.adult_count }}+{{ s.child_count }}</td>
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
</style>
