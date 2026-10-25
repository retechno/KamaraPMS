<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetPolicy } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { tierInputs, tierRowOf, type TierRow } from '@/utils/banquetMoney'
import BanquetTiersEditor from './BanquetTiersEditor.vue'

/** The cancellation policy of one event (docs/architecture/36-banquet.md 4.7): the base of the percent and the tiers. The change is for this event only and is audited; the template is not touched. */
const props = defineProps<{ open: boolean; propertyId: number; eventId: number; policy: BanquetPolicy }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; saved: [] }>()

const base = ref<'NET' | 'GROSS'>('NET')
const rows = ref<TierRow[]>([])
const error = ref<ApiError | null>(null)
const busy = ref(false)
watch(() => props.open, (open) => {
  if (!open) return
  error.value = null
  base.value = props.policy.base === 'GROSS' ? 'GROSS' : 'NET'
  rows.value = props.policy.tiers.map(tierRowOf)
})

async function save(): Promise<void> {
  busy.value = true
  error.value = null
  try {
    await api.PUT('/api/v1/properties/{propertyId}/banquet/events/{id}/policy', { params: { path: { propertyId: props.propertyId, id: props.eventId } }, body: { base: base.value, tiers: tierInputs(rows.value) } })
    emit('saved')
    emit('update:open', false)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(v) => emit('update:open', v)">
    <DialogContent class="max-h-[90dvh] max-w-2xl overflow-y-auto p-5" data-testid="policy-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="save">
        <DialogTitle>{{ t('banquet.policy.editTitle') }}</DialogTitle>
        <DialogDescription>{{ t('banquet.policy.editHint') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <FormField :label="t('banquet.policy.base')">
          <template #default="{ id }"><NativeSelect :id="id" v-model="base" name="policy_base"><option value="NET">{{ t('banquet.policy.baseNET') }}</option><option value="GROSS">{{ t('banquet.policy.baseGROSS') }}</option></NativeSelect></template>
        </FormField>
        <BanquetTiersEditor v-model="rows" />
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy" data-testid="policy-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
