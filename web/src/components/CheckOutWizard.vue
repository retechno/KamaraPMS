<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CheckOutResult, StayDetail } from '@/api/types'
import { usePropertyStore } from '@/stores/property'
import { newIdempotencyKey } from '@/utils/reservations'

const props = defineProps<{ detail: StayDetail }>()
const emit = defineEmits<{ done: [result: CheckOutResult]; cancel: [] }>()
const property = usePropertyStore()

type Step = 'review' | 'done'
const step = ref<Step>('review')
const confirmEarly = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const result = ref<CheckOutResult | null>(null)
let key = newIdempotencyKey()

const businessDate = computed(() => property.clock?.business_date ?? '')
const early = computed(() => props.detail.stay.departure_date > businessDate.value)
const unbalanced = computed(() => props.detail.folios.filter((f) => f.status === 'OPEN' && Number(f.balance) !== 0))
// The server decides what is owed after the room charge of tonight; the balance shown is the folio's today.
const blocked = computed(() => (early.value && !confirmEarly.value) || busy.value)

async function submit(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/stays/{id}/check-out', {
      params: { path: { propertyId, id: props.detail.stay.id }, header: { 'Idempotency-Key': key } },
      body: { version: props.detail.stay.version, confirm_early_departure: confirmEarly.value },
    })
    if (data) {
      result.value = data
      step.value = 'done'
      emit('done', data)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) key = newIdempotencyKey()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="card" data-testid="checkout-wizard">
    <h2>Check out</h2>
    <p v-if="error" class="alert" role="alert" data-testid="checkout-error">
      {{ error.message }} <code>{{ error.code }}</code>
      <template v-if="error.code === 'FOLIO_NOT_BALANCED'"> Settle the folio, then check out again.</template>
    </p>

    <template v-if="step === 'review'">
      <ol class="steps">
        <li :class="{ ok: !early || confirmEarly }" data-testid="step-departure">
          Departure {{ detail.stay.departure_date }}
          <template v-if="early">
            is after today: the guest leaves early and the stay ends with tonight's night.
            <label class="check"><input v-model="confirmEarly" type="checkbox" name="confirm_early" /><span>Confirm early departure</span></label>
          </template>
          <template v-else>matches the business date.</template>
        </li>
        <li :class="{ ok: !unbalanced.length }" data-testid="step-folios">
          <template v-if="!unbalanced.length">Every folio is balanced so far. Tonight's room charge, if not yet posted, is added now and must be settled too.</template>
          <template v-else>
            Folios with a balance:
            <span v-for="f in unbalanced" :key="f.id"><RouterLink :to="`/folios/${f.id}`" :data-testid="`open-folio-${f.id}`">{{ f.folio_number }}</RouterLink> ({{ f.balance }}) </span>
          </template>
        </li>
      </ol>
      <div class="form-actions">
        <button type="button" class="btn-primary" :disabled="blocked" data-testid="checkout-submit" @click="submit">Check out</button>
        <button type="button" data-testid="checkout-cancel" @click="emit('cancel')">Cancel</button>
      </div>
    </template>

    <template v-else-if="result">
      <p data-testid="checkout-done">Checked out. The room is now <strong>{{ result.housekeeping }}</strong>.</p>
      <ul>
        <li v-for="c in result.posted_room_charges" :key="c.service_date">Room charge {{ c.service_date }}: {{ c.total }}</li>
        <li v-for="f in result.folios" :key="f.id">Folio {{ f.folio_number }} {{ f.status }}</li>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.steps {
  padding-left: 20px;
  display: grid;
  gap: 8px;
}
.steps li.ok::marker {
  color: var(--accent);
}
</style>
