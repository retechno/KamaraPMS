<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { FolioSummary } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const status = ref('OPEN')
const rows = ref<FolioSummary[]>([])
const nextCursor = ref<string | undefined>()
const error = ref<ApiError | null>(null)
const loading = ref(false)
const searched = ref(false)

const canRead = computed(() => auth.can('folio.read', property.currentId))

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/folios', {
      params: { path: { propertyId }, query: { limit: 50, cursor: more ? nextCursor.value : undefined, status: (status.value || undefined) as 'OPEN' | 'CLOSED' | undefined } },
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
    <h1 class="page-title">Folios</h1>
    <RouterLink to="/cashier">Cashier</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property does not allow viewing folios.</p>

  <template v-else>
    <form class="card filters" role="search" @submit.prevent="load()">
      <label class="field">
        <span>Status</span>
        <select v-model="status" name="status">
          <option value="">Any</option>
          <option value="OPEN">Open</option>
          <option value="CLOSED">Closed</option>
        </select>
      </label>
      <button type="submit" :disabled="loading">Search</button>
    </form>
    <section class="card">
      <p v-if="searched && !rows.length" class="muted" data-testid="empty">No folios found.</p>
      <table v-else-if="rows.length" class="list">
        <thead><tr><th>Folio</th><th>Reservation</th><th>Status</th><th class="num">Balance</th></tr></thead>
        <tbody>
          <tr v-for="f in rows" :key="f.id" :data-testid="`folio-${f.folio_number}`">
            <td><RouterLink :to="`/folios/${f.id}`">{{ f.folio_number }}</RouterLink></td>
            <td><RouterLink :to="`/reservations/${f.reservation_id}`">#{{ f.reservation_id }}</RouterLink></td>
            <td>{{ f.status }}</td>
            <td class="num">{{ f.balance }}</td>
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
.filters {
  display: flex;
  gap: 12px;
  align-items: flex-end;
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
.num {
  text-align: right !important;
  font-variant-numeric: tabular-nums;
}
</style>
