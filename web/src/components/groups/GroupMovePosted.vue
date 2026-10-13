<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, MasterFolio, PostedCharges } from '@/api/types'
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
 * Moves the charges already posted to the members of a group to its master folio, right after the instructions were saved (docs/architecture/30-group-folio.md, 5.3). It is a use of the transfer of
 * part B (by charge code, scope GROUP_MASTER), one request for each folio and charge code, with the same reason and the same approval; a request that fails stops the others, and what was moved is said.
 */
const props = defineProps<{ master: MasterFolio; posted: PostedCharges[] }>()
const emit = defineEmits<{ done: [message: string]; close: [] }>()

const property = usePropertyStore()
const reason = ref('')
const stage = ref<'form' | 'approval'>('form')
const busy = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)

const total = computed(() => props.posted.reduce((n, p) => n + Number(p.total), 0))
const count = computed(() => props.posted.reduce((n, p) => n + p.count, 0))

async function approve(approval: Approval): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  busy.value = true
  dialogError.value = null
  let moved = 0
  try {
    for (const row of props.posted) {
      await api.POST('/api/v1/properties/{propertyId}/folios/{id}/transfer-items', {
        params: { path: { propertyId, id: row.folio_id } },
        body: { folio_id: props.master.id, charge_code_id: row.charge_code_id, reason: reason.value.trim(), approval },
      })
      moved += row.count
    }
    emit('done', t('groupMaster.moved', { n: moved, folio: props.master.folio_number }))
  } catch (e) {
    // a wrong password can be tried again; what was moved before the failure stays moved, and the list is read again when the dialog closes
    const err = e instanceof ApiError ? e : null
    if (moved > 0) {
      error.value = err
      stage.value = 'form'
      emit('done', t('groupMaster.movedPartly', { n: moved, folio: props.master.folio_number }))
    } else {
      dialogError.value = err
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog v-if="stage === 'form'" :open="true" @update:open="(v) => { if (!v) emit('close') }">
    <DialogContent class="max-w-lg p-5" data-testid="move-posted-dialog" @interact-outside.prevent>
      <form novalidate data-testid="move-posted-form" @submit.prevent="stage = 'approval'">
        <DialogTitle>{{ t('groupMaster.moveTitle', { folio: master.folio_number }) }}</DialogTitle>
        <DialogDescription class="mb-3 mt-1">{{ t('groupMaster.moveHint') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <ul class="m-0 mb-3 list-none divide-y divide-border p-0 text-sm" data-testid="move-posted-list">
          <li v-for="p in posted" :key="`${p.folio_id}-${p.charge_code_id}`" class="flex items-baseline justify-between gap-3 py-1.5">
            <span class="min-w-0 truncate">{{ p.folio_number }} · {{ p.confirmation_number }} · {{ p.charge_code }}</span>
            <span class="shrink-0 tabular-nums text-muted-foreground">{{ p.count }} × {{ formatMoney(p.total) }}</span>
          </li>
        </ul>
        <p class="m-0 mb-3 text-sm font-medium tabular-nums" data-testid="move-posted-total">{{ t('groupMaster.moveTotal', { n: count, total: formatMoney(String(total)) }) }}</p>
        <FormField :label="t('folioTransfer.reason')">
          <template #default="{ id }"><Input :id="id" v-model="reason" name="move_reason" maxlength="500" /></template>
        </FormField>
        <p v-if="!reason.trim()" class="m-0 mt-4 text-sm text-muted-foreground" data-testid="move-posted-missing">{{ t('folioTransfer.missing', { what: t('folioTransfer.missingReason') }) }}</p>
        <div class="mt-3 flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="move-posted-cancel" @click="emit('close')">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="!reason.trim() || busy" data-testid="move-posted-continue">{{ t('folioTransfer.continueApproval') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
  <ApprovalDialog v-else :title="t('groupMaster.moveApprovalTitle')" :busy="busy" :error="dialogError" @approve="approve" @cancel="stage = 'form'" />
</template>
