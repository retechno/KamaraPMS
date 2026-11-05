<script setup lang="ts">
/**
 * The payment step of the check-out for a guest folio that owes: amount (the balance to begin with), method (only those the folio payment endpoint takes), reference. One Idempotency-Key
 * per attempt (`createAttemptKey`): "try again" after a lost answer is the same request and cannot pay twice; the key is renewed when the server answered or the content changed. A payment
 * above the balance only warns (the folio becomes a credit). Cash goes with `ShiftRequiredNotice`.
 */
import { computed, reactive, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import ShiftRequiredNotice from '@/components/app/ShiftRequiredNotice.vue'
import OverpayNotice from '@/components/folio/OverpayNotice.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import type { PaymentMethod } from '@/api/types'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { createAttemptKey } from '@/utils/attemptKey'

const props = defineProps<{ folioId: number; balance: string }>()
const emit = defineEmits<{ paid: [] }>()

const METHODS: PaymentMethod[] = ['CASH', 'CARD', 'BANK_TRANSFER', 'OTHER', 'QRIS']
const property = usePropertyStore()
const form = reactive({ amount: props.balance, method: 'CASH' as PaymentMethod, reference: '' })
const busy = ref(false)
const error = ref<ApiError | null>(null)
const attempt = createAttemptKey()
const methodLabel = (m: string): string => t(`cashier.${m}` as never)
const canPay = computed(() => Number(form.amount) > 0)

async function pay(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || busy.value || !canPay.value) return
  busy.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/folios/{id}/payments', {
      params: { path: { propertyId, id: props.folioId }, header: { 'Idempotency-Key': attempt.keyFor([form.amount.trim(), form.method, form.reference.trim()]) } },
      body: { amount: form.amount.trim(), payment_method: form.method, reference_number: form.reference.trim() || undefined },
    })
    attempt.answered()
    form.reference = ''
    emit('paid')
  } catch (e) {
    if (e instanceof ApiError) {
      attempt.answered() // the server answered: another attempt is another request
      error.value = e
    }
    // no answer (the network dropped): the key stays, so the same content tried again is the same payment
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mt-2 grid gap-3" data-testid="checkout-pay">
    <div class="grid gap-3 sm:grid-cols-3">
      <FormField :label="t('folio.amount')">
        <template #default="{ id }"><Input :id="id" v-model="form.amount" name="pay_amount" inputmode="decimal" @keydown.enter.prevent="pay" /></template>
      </FormField>
      <FormField :label="t('folio.method')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="form.method" name="pay_method">
            <option v-for="m in METHODS" :key="m" :value="m">{{ methodLabel(m) }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField :label="t('folio.reference')">
        <template #default="{ id }"><Input :id="id" v-model="form.reference" name="pay_reference" @keydown.enter.prevent="pay" /></template>
      </FormField>
    </div>
    <ShiftRequiredNotice :method="form.method" />
    <OverpayNotice :balance="balance" :amount="form.amount" />
    <ErrorNotice :error="error" inline />
    <div><Button type="button" :disabled="busy || !canPay" data-testid="checkout-pay-submit" @click="pay">{{ t('checkout.pay') }}</Button></div>
  </div>
</template>
