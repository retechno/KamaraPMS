<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CityLedgerAccount } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const accounts = ref<CityLedgerAccount[]>([])
const nextCursor = ref<string | undefined>()
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const owingOnly = ref(true)
const q = ref('')

const canRead = computed(() => auth.can('cityledger.read', property.currentId))

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts', {
      params: { path: { propertyId }, query: { limit: 50, cursor: more ? nextCursor.value : undefined, owing: owingOnly.value ? true : undefined, q: q.value.trim() || undefined } },
    })
    accounts.value = more ? [...accounts.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

watch(() => property.currentId, () => {
  accounts.value = []
  loaded.value = false
  void load()
}, { immediate: true })
watch(owingOnly, () => void load())
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">City ledger</h1>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property cannot see the city ledger: the <code>cityledger.read</code> permission is needed.</p>

  <section v-else class="card">
    <p class="muted">What companies owe the hotel: folios moved to a company's account, less what the company has paid.</p>
    <form class="filters" novalidate data-testid="filters" @submit.prevent="load()">
      <label class="field">
        <span>Company</span>
        <input v-model="q" name="q" type="search" placeholder="Code or name" />
      </label>
      <label class="check">
        <input v-model="owingOnly" type="checkbox" name="owing" />
        <span>Only companies that owe</span>
      </label>
      <button type="submit">Search</button>
    </form>
    <p v-if="loaded && !accounts.length" class="muted" data-testid="empty">{{ owingOnly ? 'No company owes anything.' : 'No companies yet.' }}</p>
    <table v-else-if="accounts.length" class="list">
      <thead>
        <tr>
          <th>Company</th>
          <th class="num">Transferred</th>
          <th class="num">Received</th>
          <th class="num">Balance</th>
          <th class="num">Credit limit</th>
          <th class="num">Available</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="a in accounts" :key="a.company_id" :data-testid="`account-${a.code}`">
          <td>
            <RouterLink :to="`/city-ledger/${a.company_id}`"><b>{{ a.code }}</b></RouterLink> {{ a.name }}
            <small v-if="!a.is_active" class="muted">(inactive)</small>
          </td>
          <td class="num">{{ a.transferred }}</td>
          <td class="num">{{ a.received }}</td>
          <td class="num"><b>{{ a.balance }}</b></td>
          <td class="num">{{ a.credit_limit ?? 'No limit' }}</td>
          <td class="num">{{ a.available ?? '-' }}</td>
        </tr>
      </tbody>
    </table>
    <button v-if="nextCursor" type="button" data-testid="more" @click="load(true)">Load more</button>
  </section>
</template>
