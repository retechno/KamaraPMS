<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { MasterFolio } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { DEPOSIT_METHODS, type DepositMethod, isPositive } from '@/utils/checkin'
import { newIdempotencyKey } from '@/utils/reservations'

/**
 * A deposit or a guarantee of the whole group: a payment on the master folio (docs/architecture/30-group-folio.md 6.4, 14). Before the first charge of the master it is an advance deposit, after it
 * a settlement; the books decide, not this screen. A cash payment needs an open cashier shift as always (the server says NO_OPEN_SHIFT). The key stays the same for a retry of this payment, and a
 * new one is made when the server has answered, so a lost answer does not take the money twice.
 */
const props = defineProps<{ master: MasterFolio }>()
const emit = defineEmits<{ done: [message: string]; close: [] }>()

const property = usePropertyStore()
const amount = ref('')
// No default: the method of a payment is a choice, and the button says what is still needed
const method = ref<DepositMethod | ''>('')
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const missing = computed(() => {
  const list: string[] = []
  if (!isPositive(amount.value, decimals.value)) list.push(t('groupCheckIn.missingAmount'))
  if (!method.value) list.push(t('groupCheckIn.missingMethodOne'))
  return list
})
const reference = ref('')
const busy = ref(false)
const error = ref<ApiError | null>(null)
let key = newIdempotencyKey()

async function submit(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || busy.value || missing.value.length > 0 || !method.value) return
  busy.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/folios/{id}/payments', {
      params: { path: { propertyId, id: props.master.id }, header: { 'Idempotency-Key': key } },
      body: { amount: amount.value.trim(), payment_method: method.value as DepositMethod, reference_number: reference.value.trim() || undefined },
    })
    key = newIdempotencyKey()
    emit('done', t('groupCheckIn.depositRecorded', { folio: props.master.folio_number }))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) key = newIdempotencyKey() // answered: another attempt is another payment
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="true" @update:open="(v) => { if (!v) emit('close') }">
    <DialogContent class="max-w-md p-5" data-testid="group-deposit-dialog" @interact-outside.prevent>
      <form novalidate @submit.prevent="submit">
        <DialogTitle>{{ t('groupCheckIn.depositTitle', { folio: master.folio_number }) }}</DialogTitle>
        <DialogDescription class="mb-3 mt-1">{{ t('groupCheckIn.depositHint') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline data-testid="group-deposit-error" />
        <FormField :label="t('groupCheckIn.depositAmount')">
          <template #default="{ id }"><MoneyInput :id="id" v-model="amount" :decimals="decimals" name="group_deposit_amount" /></template>
        </FormField>
        <FormField :label="t('groupCheckIn.depositMethod')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="method" name="group_deposit_method">
              <option value="">{{ t('groupCheckIn.methodPick') }}</option>
              <option v-for="m in DEPOSIT_METHODS" :key="m" :value="m">{{ t(`cashier.${m}` as never) }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('groupCheckIn.depositReference')">
          <template #default="{ id }"><Input :id="id" v-model="reference" name="group_deposit_reference" maxlength="100" /></template>
        </FormField>
        <p v-if="missing.length" class="m-0 mt-3 text-sm text-muted-foreground" data-testid="group-deposit-missing">{{ t('groupCheckIn.stillNeeded', { what: missing.join(', ') }) }}</p>
        <div class="mt-3 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('close')">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || missing.length > 0" data-testid="group-deposit-save">{{ t('groupCheckIn.depositSave') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
