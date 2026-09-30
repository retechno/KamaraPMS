<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { RoomChargeItem, RoomChargePostResponse, RoomChargePreview } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { newIdempotencyKey } from '@/utils/reservations'

const auth = useAuthStore()
const property = usePropertyStore()

const preview = ref<RoomChargePreview | null>(null)
const outcome = ref<RoomChargePostResponse | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
// One key per posting attempt (the run is idempotent anyway; the header is part of the contract).
let key = newIdempotencyKey()

const pid = computed(() => property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')
const allowed = computed(() => auth.can('nightaudit.run', pid.value) || auth.can('folio.post_charge', pid.value))
const ready = computed(() => preview.value?.items.filter((i) => i.status === 'READY') ?? [])
const problems = computed(() => preview.value?.items.filter((i) => i.status === 'ERROR') ?? [])
const isMissing = (i: RoomChargeItem) => i.status === 'READY' && i.service_date < businessDate.value

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !allowed.value || !businessDate.value) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/night-audit/room-charges/preview', {
      params: { path: { propertyId } }, body: { business_date: businessDate.value },
    })
    preview.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function post(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/night-audit/room-charges', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': key } }, body: { business_date: businessDate.value },
    })
    outcome.value = data ?? null
    key = newIdempotencyKey()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) key = newIdempotencyKey()
  } finally {
    busy.value = false
  }
  await load()
}

watch([pid, businessDate], () => {
  preview.value = null
  outcome.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Room charges <span class="muted">{{ businessDate }}</span></h1>
    <button type="button" :disabled="busy" data-testid="refresh" @click="load">Refresh</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!allowed" class="muted" data-testid="no-access">Your role at this property does not allow posting room charges.</p>

  <template v-else-if="preview">
    <section v-if="outcome" class="card" data-testid="outcome">
      <h2>Posted</h2>
      <p>{{ outcome.results.filter((r) => r.status === 'POSTED').length }} night(s) posted,
        {{ outcome.results.filter((r) => r.status === 'ALREADY_POSTED').length }} already posted,
        {{ outcome.revalidation.errors.length }} with errors, {{ outcome.revalidation.ready }} still ready.</p>
      <p v-if="outcome.revalidation.invalid.length" class="alert" data-testid="invalid">
        {{ outcome.revalidation.invalid.length }} posted night(s) are outside their stay: reverse them on the folio.
      </p>
    </section>

    <section class="card">
      <div class="head">
        <span data-testid="ready-count">{{ preview.totals.ready_count }} night(s) ready</span>
        <span>Total <strong data-testid="ready-total">{{ preview.totals.ready_total }}</strong></span>
        <button v-if="preview.totals.ready_count" type="button" class="btn-primary" :disabled="busy" data-testid="post" @click="post">Post room charges</button>
      </div>
      <p v-if="!preview.items.length" class="muted" data-testid="empty">Nobody is in house.</p>
      <table v-else class="list" data-testid="items">
        <thead><tr><th>Stay</th><th>Guest</th><th>Room</th><th>Night</th><th class="num">Rate</th><th class="num">Service</th><th class="num">Tax</th><th class="num">Total</th><th>Status</th></tr></thead>
        <tbody>
          <tr v-for="i in preview.items" :key="`${i.stay_id}-${i.service_date}`" :data-testid="`item-${i.stay_id}-${i.service_date}`" :class="i.status.toLowerCase()">
            <td>{{ i.stay_number }}</td>
            <td>{{ i.guest }}</td>
            <td>{{ i.room_number || '—' }}</td>
            <td>{{ i.service_date }} <small v-if="isMissing(i)" class="muted">missing</small></td>
            <td class="num">{{ i.room_rate }}</td>
            <td class="num">{{ i.status === 'READY' ? i.service_charge : '' }}</td>
            <td class="num">{{ i.status === 'READY' ? i.tax : '' }}</td>
            <td class="num">{{ i.status === 'READY' ? i.total : '' }}</td>
            <td>{{ i.status }}<small v-if="i.reason" class="muted"> {{ i.reason }}</small></td>
          </tr>
        </tbody>
      </table>
      <p v-if="problems.length" class="alert" data-testid="problems">{{ problems.length }} night(s) cannot be posted until their problem is fixed.</p>
      <p v-if="ready.length === 0 && preview.items.length" class="muted">Nothing is due.</p>
    </section>
  </template>
</template>

<style scoped>
.head {
  display: flex;
  flex-wrap: wrap;
  gap: 24px;
  align-items: center;
  margin-bottom: 12px;
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
.already_posted td,
.not_applicable td {
  color: var(--text-muted);
}
.error td {
  color: var(--danger);
}
</style>
