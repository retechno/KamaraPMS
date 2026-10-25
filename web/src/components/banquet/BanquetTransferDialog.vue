<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, BanquetEvent, BanquetFolioRef, BanquetGroupTarget, FolioItem } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
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
import { newIdempotencyKey } from '@/utils/reservations'

/**
 * "Transfer to the master folio of a group" (docs/architecture/36-banquet.md 4.8, mockup 40). By items: every charge of the bill that nothing has reversed moves to the master folio (reversed here, charged
 * again there as a copy, so revenue and tax stay as they were). By an amount: a part of the balance moves, which keeps the revenue on the event. An event that is not linked to the group is linked first, in the same step.
 * The transfer needs a reason and the approval of the corrections (folio.reverse); the key is kept for a retry and renewed once the server has answered.
 */
const props = defineProps<{ open: boolean; propertyId: number; event: BanquetEvent; folio: BanquetFolioRef; groups: BanquetGroupTarget[] }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; done: [] }>()

const property = usePropertyStore()
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const form = ref({ group_id: 0, mode: 'ITEMS' as 'ITEMS' | 'BALANCE', amount: '', reason: '' })
const items = ref<FolioItem[]>([])
const error = ref<ApiError | null>(null)
const approving = ref(false)
const approveError = ref<ApiError | null>(null)
const busy = ref(false)
let key = newIdempotencyKey()

const linked = computed(() => props.event.booking_group_id)
const movable = computed(() => items.value.filter((i) => i.transaction_type === 'CHARGE' && i.reversed_by_item_id === null && i.reverses_item_id === null))
const movableTotal = computed(() => movable.value.reduce((sum, i) => sum + Number(i.debit) - Number(i.credit), 0))

watch(() => props.open, async (open) => {
  if (!open) return
  error.value = null
  approving.value = false
  form.value = { group_id: linked.value ?? props.groups[0]?.group_id ?? 0, mode: 'ITEMS', amount: props.folio.balance, reason: '' }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/folios/{id}', { params: { path: { propertyId: props.propertyId, id: props.folio.id } } })
    items.value = data?.items ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
})

const ready = computed(() => form.value.group_id > 0 && form.value.reason.trim() !== '' && (form.value.mode === 'ITEMS' ? movable.value.length > 0 : Number(form.value.amount) > 0))

async function submit(approval: Approval): Promise<void> {
  busy.value = true
  error.value = null
  approveError.value = null
  const f = form.value
  try {
    await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/transfer-to-group', {
      params: { path: { propertyId: props.propertyId, id: props.event.id }, header: { 'Idempotency-Key': key } },
      body: { group_id: f.group_id, mode: f.mode, item_ids: f.mode === 'ITEMS' ? movable.value.map((i) => i.id) : null, amount: f.mode === 'BALANCE' ? f.amount.trim() : '', reason: f.reason.trim(), approval },
    })
    key = newIdempotencyKey()
    approving.value = false
    emit('done')
    emit('update:open', false)
  } catch (e) {
    const err = e instanceof ApiError ? e : null
    approveError.value = err
    if (err && !['APPROVAL_INVALID', 'APPROVAL_NOT_PERMITTED', 'APPROVAL_SAME_USER'].includes(err.code)) {
      approving.value = false
      error.value = err
      key = newIdempotencyKey()
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="open && !approving" @update:open="(v) => emit('update:open', v)">
    <DialogContent class="max-h-[90dvh] max-w-xl overflow-y-auto p-5" data-testid="transfer-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="approving = true">
        <DialogTitle>{{ t('banquet.transfer.title') }}</DialogTitle>
        <DialogDescription>{{ t('banquet.transfer.intro', { folio: folio.number }) }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <p v-if="!groups.length" class="m-0 text-sm text-muted-foreground" data-testid="transfer-no-group">{{ t('banquet.transfer.noGroup') }}</p>
        <FormField :label="t('banquet.transfer.group')" required :error="error?.fieldMessage('group_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="form.group_id" name="group_id" :disabled="linked !== null">
              <option v-for="g in groups" :key="g.group_id" :value="g.group_id">{{ g.code }} · {{ g.name }} · {{ g.folio_number }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <p v-if="linked === null && form.group_id" class="m-0 text-xs text-muted-foreground" data-testid="transfer-link-note">{{ t('banquet.transfer.linkNote') }}</p>
        <fieldset class="m-0 flex flex-col gap-2 border-0 p-0">
          <label class="flex cursor-pointer items-start gap-2 rounded-md border border-border p-3 text-sm" :class="form.mode === 'ITEMS' ? 'border-primary bg-primary/5' : ''">
            <input v-model="form.mode" type="radio" name="mode" value="ITEMS" class="mt-0.5 size-4 accent-primary" data-testid="transfer-mode-items" />
            <span><span class="block font-medium">{{ t('banquet.transfer.byItems') }}</span><span class="text-muted-foreground">{{ t('banquet.transfer.byItemsHint', { n: movable.length, total: formatMoney(String(movableTotal)) }) }}</span></span>
          </label>
          <label class="flex cursor-pointer items-start gap-2 rounded-md border border-border p-3 text-sm" :class="form.mode === 'BALANCE' ? 'border-primary bg-primary/5' : ''">
            <input v-model="form.mode" type="radio" name="mode" value="BALANCE" class="mt-0.5 size-4 accent-primary" data-testid="transfer-mode-balance" />
            <span><span class="block font-medium">{{ t('banquet.transfer.byAmount') }}</span><span class="text-muted-foreground">{{ t('banquet.transfer.byAmountHint') }}</span></span>
          </label>
        </fieldset>
        <FormField v-if="form.mode === 'BALANCE'" :label="t('banquet.transfer.amount')" :error="error?.fieldMessage('amount')">
          <template #default="{ id }"><MoneyInput :id="id" v-model="form.amount" :decimals="decimals" name="amount" /></template>
        </FormField>
        <FormField :label="t('banquet.transfer.reason')" required :error="error?.fieldMessage('reason')">
          <template #default="{ id }"><Input :id="id" v-model="form.reason" name="reason" maxlength="500" /></template>
        </FormField>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="!ready" data-testid="transfer-next">{{ t('banquet.transfer.action') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
  <ApprovalDialog
    v-if="open && approving" kind="FOLIO_TRANSFER" :title="t('banquet.transfer.approvalTitle')" :message="t('banquet.transfer.approvalMessage')" :busy="busy" :error="approveError"
    @approve="(a) => submit(a)" @cancel="approving = false"
  />
</template>
