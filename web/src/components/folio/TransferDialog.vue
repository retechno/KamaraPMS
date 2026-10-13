<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, Folio, FolioItem, TransferResult, TransferTarget } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'
import { newIdempotencyKey } from '@/utils/reservations'

/**
 * The transfer dialog of a folio (docs/architecture/30-group-folio.md, part B): the charges that are selected (or every charge of one code) go to another folio, or part of the balance does. The target is
 * found by the number of its folio, its room or its guest (the server searches); it says how the folio is related (the same reservation, the same group, no relation: that one needs a right of its own). A
 * transfer is a correction: a reason, then the approval. A balance transfer warns that the tax invoice stays with this folio.
 */
const props = defineProps<{ folio: Folio; items: FolioItem[]; codeId?: number; tab: 'items' | 'balance' }>()
const emit = defineEmits<{ done: [message: string]; close: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()

const tab = ref<'items' | 'balance'>(props.tab)
const targets = ref<TransferTarget[]>([])
const chosen = ref<TransferTarget | null>(null)
const target = ref<number>(0)
const amount = ref(Number(props.folio.balance) > 0 ? props.folio.balance : '')
const reason = ref('')
const stage = ref<'form' | 'approval'>('form')
const busy = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
let balanceKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const total = computed(() => props.items.reduce((n, i) => n + Number(i.debit), 0))
const byCode = computed(() => props.codeId !== undefined && props.codeId > 0)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const scopeText = (scope: string): string => t(`folioTransfer.scope.${scope}` as never)

const label = (x: TransferTarget): string => [x.folio_number, x.room, x.guest, x.company].filter(Boolean).join(' · ')
const options = computed(() => {
  const list = targets.value.map((x) => ({ value: x.id, label: `${label(x)} (${scopeText(x.scope)})` }))
  if (chosen.value && !targets.value.some((x) => x.id === chosen.value?.id)) list.unshift({ value: chosen.value.id, label: `${label(chosen.value)} (${scopeText(chosen.value.scope)})` })
  return [{ value: 0, label: t('folioTransfer.chooseFolio') }, ...list]
})
watch(target, (id) => {
  chosen.value = targets.value.find((x) => x.id === id) ?? (id === 0 ? null : chosen.value)
})

let timer: ReturnType<typeof setTimeout> | undefined
async function search(q: string): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/folios/{id}/transfer-targets', { params: { path: { propertyId, id: props.folio.id }, query: { q: q.trim() || undefined } } })
    targets.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
function onSearch(q: string): void {
  clearTimeout(timer)
  timer = setTimeout(() => void search(q), 250)
}
void search('')
onBeforeUnmount(() => clearTimeout(timer))

const balanceLeft = computed(() => Number(props.folio.balance))
const canContinue = computed(() => {
  if (target.value <= 0 || !reason.value.trim() || busy.value) return false
  return tab.value === 'items' ? props.items.length > 0 : Number(amount.value) > 0
})
const needsAny = computed(() => chosen.value?.scope === 'ANY')
const canAny = computed(() => auth.can('folio.transfer_any', pid.value))

function start(): void {
  dialogError.value = null
  error.value = null
  stage.value = 'approval'
}

async function approve(approval: Approval): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  dialogError.value = null
  try {
    let result: TransferResult | undefined
    if (tab.value === 'items') {
      const body = byCode.value
        ? { folio_id: target.value, charge_code_id: props.codeId, reason: reason.value.trim(), approval }
        : { folio_id: target.value, item_ids: props.items.map((i) => i.id), reason: reason.value.trim(), approval }
      result = (await api.POST('/api/v1/properties/{propertyId}/folios/{id}/transfer-items', { params: { path: { propertyId, id: props.folio.id } }, body })).data
      emit('done', t('folioTransfer.doneItems', { n: result?.transfers.length ?? 0, folio: chosen.value?.folio_number ?? '' }))
    } else {
      result = (await api.POST('/api/v1/properties/{propertyId}/folios/{id}/transfer-balance', {
        params: { path: { propertyId, id: props.folio.id }, header: { 'Idempotency-Key': balanceKey } },
        body: { folio_id: target.value, amount: String(amount.value).trim(), reason: reason.value.trim(), approval },
      })).data
      balanceKey = newIdempotencyKey()
      emit('done', t('folioTransfer.doneBalance', { amount: formatMoney(String(amount.value)), folio: chosen.value?.folio_number ?? '' }))
    }
  } catch (e) {
    // the dialog stays open with what was typed; a wrong password can be tried again
    dialogError.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError && tab.value === 'balance') balanceKey = newIdempotencyKey()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog v-if="stage === 'form'" :open="true" @update:open="(v) => { if (!v) emit('close') }">
    <DialogContent class="max-w-xl p-5" data-testid="transfer-dialog" @interact-outside.prevent>
      <form novalidate data-testid="transfer-folio-form" @submit.prevent="start">
        <DialogTitle>{{ t('folioTransfer.title', { folio: folio.folio_number }) }}</DialogTitle>
        <DialogDescription class="mb-3 mt-1">{{ t('folioTransfer.hint') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline data-testid="transfer-error" />

        <Tabs :model-value="tab" @update:model-value="tab = String($event) as 'items' | 'balance'">
          <TabsList data-testid="transfer-tabs">
            <TabsTrigger value="items" data-testid="tab-transfer-items">{{ t('folioTransfer.tabItems') }}</TabsTrigger>
            <TabsTrigger value="balance" data-testid="tab-transfer-balance">{{ t('folioTransfer.tabBalance') }}</TabsTrigger>
          </TabsList>

          <TabsContent value="items" class="pt-3">
            <p v-if="byCode" class="m-0 text-sm" data-testid="transfer-selection">{{ t('folioTransfer.selectedByCode', { n: items.length, total: formatMoney(String(total)) }) }}</p>
            <p v-else-if="items.length" class="m-0 text-sm" data-testid="transfer-selection">{{ t('folioTransfer.selected', { n: items.length, total: formatMoney(String(total)) }) }}</p>
            <p v-else class="m-0 text-sm text-muted-foreground" data-testid="transfer-selection-none">{{ t('folioTransfer.selectNone') }}</p>
            <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('folioTransfer.itemsHint') }}</p>
          </TabsContent>

          <TabsContent value="balance" class="pt-3">
            <FormField :label="t('folioTransfer.amount', { balance: formatMoney(folio.balance) })" :error="fieldError('amount')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="amount" name="transfer_balance_amount" inputmode="decimal" :aria-invalid="invalid" /></template>
            </FormField>
            <p v-if="balanceLeft <= 0" class="m-0 mt-1 text-sm text-warning-text" data-testid="transfer-no-balance">{{ t('folioTransfer.noBalance') }}</p>
            <p class="alert warning mt-3" data-testid="tax-warning">{{ t('folioTransfer.taxWarning') }}</p>
          </TabsContent>
        </Tabs>

        <div class="mt-3 grid gap-3">
          <FormField :label="t('folioTransfer.target')" :error="fieldError('folio_id')">
            <template #default="{ id }">
              <Combobox :id="id" v-model="target" name="transfer_target" :options="options" :placeholder="t('folioTransfer.chooseFolio')" @search="onSearch" />
            </template>
          </FormField>
          <p v-if="chosen" class="m-0 flex flex-wrap items-center gap-2 text-sm" data-testid="target-scope">
            <Badge :variant="chosen.scope === 'ANY' ? 'warning' : 'outline'">{{ scopeText(chosen.scope) }}</Badge>
            <span class="text-muted-foreground">{{ t('folioTransfer.targetBalance', { balance: formatMoney(chosen.balance) }) }}</span>
          </p>
          <p v-if="needsAny" class="alert warning m-0" data-testid="any-warning">{{ canAny ? t('folioTransfer.anyWarning') : t('folioTransfer.anyDenied') }}</p>
          <FormField :label="t('folioTransfer.reason')" :error="fieldError('reason')">
            <template #default="{ id }"><Input :id="id" v-model="reason" name="transfer_reason" maxlength="500" /></template>
          </FormField>
        </div>

        <div class="mt-5 flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="transfer-cancel" @click="emit('close')">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="!canContinue" data-testid="transfer-continue">{{ t('folioTransfer.continueApproval') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
  <ApprovalDialog
    v-else :title="t('folioTransfer.approvalTitle')" :message="needsAny ? t('folioTransfer.approvalAny') : undefined" :busy="busy" :error="dialogError"
    @approve="approve" @cancel="stage = 'form'"
  />
</template>
