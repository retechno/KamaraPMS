<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetEvent, BanquetFolioRef } from '@/api/types'
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
 * "Receive a down payment" (docs/architecture/36-banquet.md 4.2, Q2): an inquiry or a tentative event takes money before it is confirmed. Two steps, two transactions: the EVENT folio is opened (asking again
 * returns the one that is open) and then the payment is posted on it with the common payment of the folios. A payment made before the first charge is a deposit. If the second step fails, the folio stays open and
 * the dialog says so; the Idempotency-Key stays the same for a retry and is renewed when the server has answered, so a lost answer does not take the money twice.
 */
const props = defineProps<{ open: boolean; propertyId: number; event: BanquetEvent; folio: BanquetFolioRef | null }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; saved: [] }>()

const property = usePropertyStore()
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const amount = ref('')
const method = ref<DepositMethod | ''>('')
const reference = ref('')
const error = ref<ApiError | null>(null)
const busy = ref(false)
const opened = ref<BanquetFolioRef | null>(null)
let key = newIdempotencyKey()
watch(() => props.open, (open) => {
  if (!open) return
  error.value = null
  amount.value = ''
  method.value = ''
  reference.value = ''
  opened.value = null
})
const missing = computed(() => {
  const list: string[] = []
  if (!isPositive(amount.value, decimals.value)) list.push(t('groupCheckIn.missingAmount'))
  if (!method.value) list.push(t('groupCheckIn.missingMethodOne'))
  return list
})

async function submit(): Promise<void> {
  if (busy.value || missing.value.length > 0 || !method.value) return
  busy.value = true
  error.value = null
  try {
    let folio = props.folio ?? opened.value
    if (!folio) {
      const { data } = await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/folio', { params: { path: { propertyId: props.propertyId, id: props.event.id } } })
      if (!data) return
      folio = opened.value = data
    }
    await api.POST('/api/v1/properties/{propertyId}/folios/{id}/payments', {
      params: { path: { propertyId: props.propertyId, id: folio.id }, header: { 'Idempotency-Key': key } },
      body: { amount: amount.value.trim(), payment_method: method.value as DepositMethod, reference_number: reference.value.trim() || undefined },
    })
    key = newIdempotencyKey()
    emit('saved')
    emit('update:open', false)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) key = newIdempotencyKey()
    if (opened.value) emit('saved') // the folio was opened even though the payment was not made: the page shows it
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(v) => emit('update:open', v)">
    <DialogContent class="max-w-md p-5" data-testid="receive-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="submit">
        <DialogTitle>{{ t('banquet.deposit.receiveTitle') }}</DialogTitle>
        <DialogDescription>{{ folio || opened ? t('banquet.deposit.receiveOnFolio', { folio: (folio ?? opened)!.number }) : t('banquet.deposit.receiveOpens') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline data-testid="receive-error" />
        <p v-if="opened && error" class="m-0 text-sm text-muted-foreground" data-testid="receive-folio-kept">{{ t('banquet.deposit.folioKept', { folio: opened.number }) }}</p>
        <FormField :label="t('groupCheckIn.depositAmount')">
          <template #default="{ id }"><MoneyInput :id="id" v-model="amount" :decimals="decimals" name="receive_amount" /></template>
        </FormField>
        <FormField :label="t('groupCheckIn.depositMethod')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="method" name="receive_method">
              <option value="">{{ t('groupCheckIn.methodPick') }}</option>
              <option v-for="m in DEPOSIT_METHODS" :key="m" :value="m">{{ t(`cashier.${m}` as never) }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('groupCheckIn.depositReference')"><template #default="{ id }"><Input :id="id" v-model="reference" name="receive_reference" maxlength="100" /></template></FormField>
        <p v-if="missing.length" class="m-0 text-sm text-muted-foreground" data-testid="receive-missing">{{ t('groupCheckIn.stillNeeded', { what: missing.join(', ') }) }}</p>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || missing.length > 0" data-testid="receive-save">{{ t('banquet.deposit.receiveAction') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
