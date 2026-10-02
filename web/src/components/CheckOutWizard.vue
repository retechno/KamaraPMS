<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CheckOutResult, StayDetail } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
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
  <Card class="mb-4" data-testid="checkout-wizard">
    <CardHeader><CardTitle>{{ t('checkout.title') }}</CardTitle></CardHeader>
    <CardContent>
      <p v-if="error" class="alert" role="alert" data-testid="checkout-error">
        {{ error.message }} <code>{{ error.code }}</code>
        <template v-if="error.code === 'FOLIO_NOT_BALANCED'"> {{ t('checkout.notBalanced') }}</template>
      </p>

      <template v-if="step === 'review'">
        <ol class="m-0 grid gap-2 pl-5">
          <li :class="{ 'marker:text-primary': !early || confirmEarly }" data-testid="step-departure">
            {{ t('checkout.departure', { date: detail.stay.departure_date }) }}
            <template v-if="early">
              {{ t('checkout.early') }}
              <label class="ml-2 inline-flex items-center gap-1.5 text-sm"><input v-model="confirmEarly" type="checkbox" name="confirm_early" class="size-4 accent-primary" /><span>{{ t('checkout.confirmEarly') }}</span></label>
            </template>
            <template v-else>{{ t('checkout.matches') }}</template>
          </li>
          <li :class="{ 'marker:text-primary': !unbalanced.length }" data-testid="step-folios">
            <template v-if="!unbalanced.length">{{ t('checkout.balanced') }}</template>
            <template v-else>
              {{ t('checkout.withBalance') }}
              <span v-for="f in unbalanced" :key="f.id"><RouterLink :to="`/folios/${f.id}`" class="text-primary hover:underline" :data-testid="`open-folio-${f.id}`">{{ f.folio_number }}</RouterLink> ({{ f.balance }}) </span>
            </template>
          </li>
        </ol>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="checkout-cancel" @click="emit('cancel')">{{ t('common.cancel') }}</Button>
          <Button type="button" :disabled="blocked" data-testid="checkout-submit" @click="submit">{{ t('checkout.submit') }}</Button>
        </div>
      </template>

      <template v-else-if="result">
        <p class="mt-0" data-testid="checkout-done">{{ t('checkout.done') }} <strong>{{ result.housekeeping }}</strong>.</p>
        <ul class="m-0 pl-5 text-sm">
          <li v-for="c in result.posted_room_charges" :key="c.service_date">{{ t('checkout.roomCharge', { date: c.service_date, total: c.total }) }}</li>
          <li v-for="f in result.folios" :key="f.id">{{ t('checkout.folioStatus', { number: f.folio_number, status: f.status }) }}</li>
        </ul>
      </template>
    </CardContent>
  </Card>
</template>
