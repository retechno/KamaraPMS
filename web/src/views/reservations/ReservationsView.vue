<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { ReservationSummary } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { statusLabel } from '@/utils/reservations'

const auth = useAuthStore()
const property = usePropertyStore()

const filter = reactive({ q: '', status: '', arrivalFrom: '', arrivalTo: '' })
const rows = ref<ReservationSummary[]>([])
const nextCursor = ref<string | undefined>()
const loading = ref(false)
const searched = ref(false)
const error = ref<ApiError | null>(null)

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const canCreate = computed(() => auth.can('reservation.create', property.currentId))

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations', {
      params: {
        path: { propertyId },
        query: {
          limit: 50,
          cursor: more ? nextCursor.value : undefined,
          q: filter.q.trim() || undefined,
          status: (filter.status || undefined) as 'DRAFT' | 'CONFIRMED' | 'CANCELLED' | undefined,
          arrival_from: filter.arrivalFrom || undefined,
          arrival_to: filter.arrivalTo || undefined,
        },
      },
    })
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
    searched.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

watch(() => property.currentId, () => {
  rows.value = []
  searched.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Reservations</h1>
    <div class="actions">
      <RouterLink to="/reservations/tape">Tape chart</RouterLink>
      <RouterLink v-if="canCreate" to="/reservations/new" class="btn-primary" data-testid="new">New reservation</RouterLink>
    </div>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property does not allow viewing reservations.</p>

  <template v-else>
    <form class="card filters" role="search" novalidate @submit.prevent="load()">
      <label class="field grow">
        <span>Search</span>
        <input v-model="filter.q" name="q" type="search" placeholder="Confirmation number or booker name" />
      </label>
      <label class="field">
        <span>Status</span>
        <select v-model="filter.status" name="status">
          <option value="">Any</option>
          <option value="DRAFT">Draft</option>
          <option value="CONFIRMED">Confirmed</option>
          <option value="CANCELLED">Cancelled</option>
        </select>
      </label>
      <label class="field">
        <span>Arrival from</span>
        <input v-model="filter.arrivalFrom" name="arrival_from" type="date" />
      </label>
      <label class="field">
        <span>Arrival to</span>
        <input v-model="filter.arrivalTo" name="arrival_to" type="date" />
      </label>
      <button type="submit" :disabled="loading">Search</button>
    </form>

    <section class="card">
      <p v-if="searched && !rows.length" class="muted" data-testid="empty">No reservations found.</p>
      <table v-else-if="rows.length" class="list">
        <thead>
          <tr>
            <th>Confirmation</th>
            <th>Booker</th>
            <th>Company / group</th>
            <th>Arrival</th>
            <th>Departure</th>
            <th>Rooms</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in rows" :key="r.id" :data-testid="`res-${r.confirmation_number}`">
            <td><RouterLink :to="`/reservations/${r.id}`">{{ r.confirmation_number }}</RouterLink></td>
            <td>{{ r.guest_name || '—' }}</td>
            <td>{{ [r.company_name, r.group_code].filter(Boolean).join(' · ') || '—' }}</td>
            <td>{{ r.arrival_date }}</td>
            <td>{{ r.departure_date }}</td>
            <td>{{ r.room_count }}</td>
            <td><span class="badge" :class="r.status.toLowerCase()">{{ statusLabel(r.status) }}</span></td>
          </tr>
        </tbody>
      </table>
      <div v-if="nextCursor" class="form-actions">
        <button type="button" :disabled="loading" data-testid="more" @click="load(true)">Load more</button>
      </div>
    </section>
  </template>
</template>

<style scoped>
.actions {
  display: flex;
  gap: 16px;
  align-items: center;
}
.filters {
  display: flex;
  gap: 12px;
  align-items: flex-end;
  flex-wrap: wrap;
}
.grow {
  flex: 1;
  min-width: 200px;
}
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
  padding: 2px 10px;
  font-size: 12px;
  background: var(--accent-soft);
}
.badge.cancelled {
  background: #fde8e8;
}
.badge.draft {
  background: #eef0f3;
}
</style>
