<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, FolioTransfer } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'

/**
 * Send transfers back (docs/architecture/30-group-folio.md, part B): one or several, from the folio that holds them now, to the folio each came from last or to the first folio it came from. A return is a
 * transfer of its own and always needs a reason and an approval. A balance goes back to the folio it left only. A closed folio on the way is refused by the server, with its number.
 */
const props = defineProps<{ holderId: number; holderNumber: string; rows: FolioTransfer[] }>()
const emit = defineEmits<{ done: [message: string]; close: [] }>()

const property = usePropertyStore()
const to = ref<'PREVIOUS' | 'ORIGIN'>('PREVIOUS')
const reason = ref('')
const stage = ref<'form' | 'approval'>('form')
const busy = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)

const hasBalance = computed(() => props.rows.some((r) => r.kind === 'BALANCE'))
const total = computed(() => props.rows.reduce((n, r) => n + Number(r.amount), 0))
const targetOf = (r: FolioTransfer): string => (to.value === 'ORIGIN' ? r.origin.number : (r.head_previous?.number ?? r.previous.number))
const closedFolio = computed(() => {
  const c = (dialogError.value ?? error.value)?.context as Record<string, unknown> | undefined
  return dialogError.value?.code === 'FOLIO_CLOSED' || error.value?.code === 'FOLIO_CLOSED' ? String(c?.folio_number ?? '') : ''
})

async function approve(approval: Approval): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  busy.value = true
  dialogError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/folios/{id}/transfers/return', {
      params: { path: { propertyId, id: props.holderId } },
      body: { transfer_ids: props.rows.map((r) => r.head_transfer_id), to: hasBalance.value ? 'PREVIOUS' : to.value, reason: reason.value.trim(), approval },
    })
    emit('done', t('folioTransfer.doneReturn', { n: data?.transfers.length ?? props.rows.length }))
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError && e.code === 'FOLIO_CLOSED') {
      error.value = e
      stage.value = 'form' // say which folio is closed, and let the other way be chosen
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog v-if="stage === 'form'" :open="true" @update:open="(v) => { if (!v) emit('close') }">
    <DialogContent class="max-w-xl p-5" data-testid="return-dialog" @interact-outside.prevent>
      <form novalidate data-testid="return-form" @submit.prevent="stage = 'approval'">
        <DialogTitle>{{ t('folioTransfer.returnTitle', { n: rows.length }) }}</DialogTitle>
        <DialogDescription class="mb-3 mt-1">{{ t('folioTransfer.returnHint', { folio: holderNumber }) }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline data-testid="return-error" />
        <p v-if="closedFolio" class="alert warning" data-testid="return-closed">{{ t('folioTransfer.returnClosed', { folio: closedFolio }) }}</p>

        <ul class="m-0 mb-3 list-none divide-y divide-border p-0 text-sm" data-testid="return-list">
          <li v-for="r in rows" :key="r.head_transfer_id" class="flex items-baseline justify-between gap-3 py-1.5" :data-testid="`return-row-${r.head_transfer_id}`">
            <span class="min-w-0 truncate">{{ r.kind === 'BALANCE' ? t('folioTransfer.balanceLine') : r.description }}</span>
            <span class="shrink-0 tabular-nums text-muted-foreground">{{ formatMoney(r.amount) }} → {{ targetOf(r) }}</span>
          </li>
        </ul>
        <p class="m-0 mb-3 text-sm font-medium tabular-nums" data-testid="return-total">{{ t('folioTransfer.returnTotal', { total: formatMoney(String(total)) }) }}</p>

        <fieldset class="m-0 mb-3 border-0 p-0" data-testid="return-to">
          <legend class="mb-1 text-sm font-medium">{{ t('folioTransfer.returnTo') }}</legend>
          <label class="mr-4 inline-flex items-center gap-2 text-sm"><input v-model="to" type="radio" name="return_to" value="PREVIOUS" class="accent-primary" />{{ t('folioTransfer.toPrevious') }}</label>
          <label class="inline-flex items-center gap-2 text-sm" :class="hasBalance && 'text-muted-foreground'">
            <input v-model="to" type="radio" name="return_to" value="ORIGIN" :disabled="hasBalance" class="accent-primary" />{{ t('folioTransfer.toOrigin') }}
          </label>
          <p v-if="hasBalance" class="m-0 mt-1 text-xs text-muted-foreground" data-testid="return-balance-note">{{ t('folioTransfer.returnBalanceNote') }}</p>
        </fieldset>

        <FormField :label="t('folioTransfer.reason')">
          <template #default="{ id }"><Input :id="id" v-model="reason" name="return_reason" maxlength="500" /></template>
        </FormField>
        <p v-if="!reason.trim()" class="m-0 mt-4 text-sm text-muted-foreground" data-testid="return-missing">{{ t('folioTransfer.missing', { what: t('folioTransfer.missingReason') }) }}</p>
        <div class="mt-3 flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="return-cancel" @click="emit('close')">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="!reason.trim() || busy" data-testid="return-continue">{{ t('folioTransfer.continueApproval') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
  <ApprovalDialog v-else :title="t('folioTransfer.returnApprovalTitle')" :busy="busy" :error="dialogError" @approve="approve" @cancel="stage = 'form'" />
</template>
