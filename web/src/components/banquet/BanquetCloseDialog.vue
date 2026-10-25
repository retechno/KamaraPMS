<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetCancelQuote, BanquetCancelResult, BanquetEvent, BanquetReason } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'

/**
 * Loses or cancels an event (docs/architecture/36-banquet.md 4.7, mockup 45). A definite event pays the fee of its tier: the dialog reads the quote of today, shows the tier, the fee, the deposits held and what
 * would be left, and the person may change the amount (a lower amount needs its reason). An event that is not definite has no tier: it can keep up to the deposit received as a forfeit. The deposit that is
 * left is not refunded here: it stays on the folio until the refund of the folios is made (it needs its own approval).
 */
const props = defineProps<{ open: boolean; propertyId: number; event: BanquetEvent; as: 'lost' | 'cancel'; reasons: BanquetReason[] }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; done: [result: BanquetCancelResult] }>()

const property = usePropertyStore()
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const quote = ref<BanquetCancelQuote | null>(null)
const form = ref({ reason_id: 0, note: '', amount: '', fee_reason: '', forfeit: false })
const error = ref<ApiError | null>(null)
const busy = ref(false)

const definite = computed(() => props.event.status === 'DEFINITE')
const reasonOptions = computed(() => props.reasons.filter((r) => r.is_active && r.kind === (props.as === 'lost' ? 'LOST' : 'CANCEL')))
const held = computed(() => quote.value?.deposits_held ?? '0')
const hasDeposit = computed(() => Number(held.value) > 0)
const lower = computed(() => definite.value && quote.value !== null && form.value.amount !== '' && Number(form.value.amount) < Number(quote.value.fee))

watch(() => props.open, async (open) => {
  if (!open) return
  error.value = null
  quote.value = null
  form.value = { reason_id: 0, note: '', amount: '', fee_reason: '', forfeit: false }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/events/{id}/cancellation-quote', { params: { path: { propertyId: props.propertyId, id: props.event.id } } })
    quote.value = data ?? null
    if (data && definite.value) form.value.amount = data.fee
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
})

async function submit(): Promise<void> {
  busy.value = true
  error.value = null
  const params = { path: { propertyId: props.propertyId, id: props.event.id } }
  const forfeit = !definite.value && form.value.forfeit
  const body = {
    reason_id: form.value.reason_id, reason_note: form.value.note.trim() || null,
    amount: definite.value || forfeit ? form.value.amount.trim() || null : null,
    action: (forfeit ? 'FORFEIT' : definite.value ? 'FEE' : '') as 'FEE' | 'FORFEIT' | '', fee_reason: lower.value ? form.value.fee_reason.trim() || null : null,
  }
  try {
    const { data } = props.as === 'lost'
      ? await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/lost', { params, body })
      : await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/cancel', { params, body })
    if (data) emit('done', data)
    emit('update:open', false)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const title = computed(() => (props.as === 'lost' ? t('banquet.event.markLost') : t('banquet.event.cancel')))
</script>

<template>
  <Dialog :open="open" @update:open="(v) => emit('update:open', v)">
    <DialogContent class="max-h-[90dvh] max-w-xl overflow-y-auto p-5" data-testid="close-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="submit">
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>{{ event.number }} · {{ event.name }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <section v-if="quote && definite" class="rounded-md border border-border p-3 text-sm" data-testid="close-quote">
          <p v-if="!quote.policy_defined" class="m-0 text-muted-foreground" data-testid="close-no-policy">{{ t('banquet.cancel.noPolicy') }}</p>
          <template v-else>
            <p class="m-0">{{ t('banquet.cancel.daysBefore', { n: quote.days_before }) }} · {{ t(`banquet.policy.base${quote.base}` as never) }} {{ formatMoney(quote.base_amount) }}</p>
            <p v-if="quote.tier" class="m-0 font-medium" data-testid="close-tier">{{ t('banquet.cancel.tier', { percent: quote.tier.percent, fee: formatMoney(quote.fee) }) }}</p>
            <p v-else class="m-0 text-muted-foreground">{{ t('banquet.cancel.noTier') }}</p>
          </template>
        </section>
        <section v-if="quote && hasDeposit" class="grid grid-cols-3 gap-3 rounded-md border border-border p-3 text-sm" data-testid="close-deposit">
          <div><div class="text-muted-foreground">{{ t('banquet.cancel.held') }}</div><div class="font-medium">{{ formatMoney(quote.deposits_held) }}</div></div>
          <div><div class="text-muted-foreground">{{ t('banquet.cancel.applied') }}</div><div class="font-medium" data-testid="close-applied">{{ formatMoney(quote.applied) }}</div></div>
          <div><div class="text-muted-foreground">{{ t('banquet.cancel.left') }}</div><div class="font-medium" data-testid="close-left">{{ formatMoney(quote.refundable) }}</div></div>
        </section>
        <FormField :label="t('banquet.event.reason')" required :error="error?.fieldMessage('reason_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="form.reason_id" name="reason_id">
              <option :value="0">{{ t('banquet.event.choose') }}</option>
              <option v-for="r in reasonOptions" :key="r.id" :value="r.id">{{ r.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('banquet.event.reasonNote')"><template #default="{ id }"><Input :id="id" v-model="form.note" name="reason_note" maxlength="500" /></template></FormField>
        <template v-if="definite">
          <FormField :label="t('banquet.cancel.amount')" :error="error?.fieldMessage('amount')">
            <template #default="{ id }"><MoneyInput :id="id" v-model="form.amount" :decimals="decimals" name="amount" /></template>
          </FormField>
          <FormField v-if="lower" :label="t('banquet.cancel.feeReason')" required :error="error?.fieldMessage('fee_reason')">
            <template #default="{ id }"><Input :id="id" v-model="form.fee_reason" name="fee_reason" maxlength="200" /></template>
          </FormField>
        </template>
        <template v-else-if="hasDeposit">
          <label class="flex cursor-pointer items-start gap-2 text-sm" data-testid="close-forfeit">
            <input v-model="form.forfeit" type="checkbox" name="forfeit" class="mt-0.5 size-4 accent-primary" />
            <span>{{ t('banquet.cancel.forfeit') }}</span>
          </label>
          <FormField v-if="form.forfeit" :label="t('banquet.cancel.forfeitAmount')" :error="error?.fieldMessage('amount')">
            <template #default="{ id }"><MoneyInput :id="id" v-model="form.amount" :decimals="decimals" name="amount" /></template>
          </FormField>
        </template>
        <p v-if="hasDeposit" class="m-0 text-xs text-muted-foreground" data-testid="close-refund-note">{{ t('banquet.cancel.refundNote') }}</p>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('common.back') }}</Button>
          <Button type="submit" variant="destructive" :disabled="busy || form.reason_id === 0" data-testid="close-save">{{ title }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
