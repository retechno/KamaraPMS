<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { MethodTotal, Payment } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const date = ref('')
const method = ref('')
const rows = ref<Payment[]>([])
const totals = ref<MethodTotal[]>([])
const nextCursor = ref<string | undefined>()
const error = ref<ApiError | null>(null)
const loading = ref(false)
const searched = ref(false)

const canRead = computed(() => auth.can('folio.read', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  if (!date.value) date.value = businessDate.value
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payments', {
      params: {
        path: { propertyId },
        query: { limit: 50, cursor: more ? nextCursor.value : undefined, business_date: date.value || undefined, method: (method.value || undefined) as 'CASH' | undefined },
      },
    })
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    if (!more) totals.value = data?.totals ?? []
    nextCursor.value = data?.next_cursor
    searched.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

watch(() => property.currentId, () => {
  date.value = ''
  rows.value = []
  searched.value = false
  void load()
}, { immediate: true })
watch(businessDate, () => {
  if (!searched.value) void load()
})
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Cashier</h1>
    <RouterLink to="/folios">Folios</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property does not allow viewing payments.</p>

  <template v-else>
    <form class="card filters" role="search" @submit.prevent="load()">
      <label class="field">
        <span>Business date</span>
        <input v-model="date" name="business_date" type="date" />
      </label>
      <label class="field">
        <span>Method</span>
        <select v-model="method" name="method">
          <option value="">Any</option>
          <option value="CASH">Cash</option>
          <option value="CARD">Card</option>
          <option value="BANK_TRANSFER">Bank transfer</option>
          <option value="OTHER">Other</option>
        </select>
      </label>
      <button type="submit" :disabled="loading">Show</button>
    </form>

    <section v-if="totals.length" class="card" data-testid="totals">
      <h2>Totals</h2>
      <table class="list">
        <thead><tr><th>Method</th><th class="num">Paid</th><th class="num">Refunded</th><th class="num">Net</th></tr></thead>
        <tbody>
          <tr v-for="t in totals" :key="t.payment_method" :data-testid="`total-${t.payment_method}`">
            <td>{{ t.payment_method }}</td><td class="num">{{ t.paid }}</td><td class="num">{{ t.refunded }}</td><td class="num">{{ t.net }}</td>
          </tr>
        </tbody>
      </table>
    </section>

    <section class="card">
      <p v-if="searched && !rows.length" class="muted" data-testid="empty">No payments found.</p>
      <table v-else-if="rows.length" class="list">
        <thead><tr><th>Number</th><th>Type</th><th>Method</th><th class="num">Amount</th><th>Status</th><th>Folio</th></tr></thead>
        <tbody>
          <tr v-for="p in rows" :key="p.id" :data-testid="`payment-${p.payment_number}`" :class="{ struck: p.status === 'VOIDED' }">
            <td>{{ p.payment_number }}</td>
            <td>{{ p.payment_type }}</td>
            <td>{{ p.payment_method }}</td>
            <td class="num">{{ p.amount }}</td>
            <td>{{ p.status }}</td>
            <td><RouterLink :to="`/folios/${p.folio_id}`">#{{ p.folio_id }}</RouterLink></td>
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
  flex-wrap: wrap;
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
.struck td {
  color: var(--text-muted);
  text-decoration: line-through;
}
</style>
