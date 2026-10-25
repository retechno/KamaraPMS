<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetPolicyTemplate } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { tierInputs, tierRowOf, type TierRow } from '@/utils/banquetMoney'
import BanquetTiersEditor from './BanquetTiersEditor.vue'

/** A policy template (docs/architecture/36-banquet.md 3.3): the deposits the property asks for and the tiers of the fee for a cancellation. An event copies it when it is confirmed; a change here changes no event. */
type Dep = { label: string; days_before: number | null; percent: string }
const props = defineProps<{ open: boolean; propertyId: number; template: BanquetPolicyTemplate | null }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; saved: [] }>()

const form = ref({ code: '', name: '', base: 'NET' as 'NET' | 'GROSS', is_default: false, is_active: true })
const deposits = ref<Dep[]>([])
const tiers = ref<TierRow[]>([])
const error = ref<ApiError | null>(null)
const busy = ref(false)
watch(() => props.open, (open) => {
  if (!open) return
  error.value = null
  const x = props.template
  form.value = { code: x?.code ?? '', name: x?.name ?? '', base: x?.base ?? 'NET', is_default: x?.is_default ?? false, is_active: x?.is_active ?? true }
  deposits.value = (x?.deposits ?? []).map((d) => ({ label: d.label, days_before: d.days_before ?? null, percent: d.percent }))
  tiers.value = (x?.tiers ?? []).map(tierRowOf)
})
const addDeposit = (): void => { deposits.value = [...deposits.value, { label: '', days_before: null, percent: '' }] }
const removeDeposit = (i: number): void => { deposits.value = deposits.value.filter((_, at) => at !== i) }

async function save(): Promise<void> {
  busy.value = true
  error.value = null
  const body = {
    code: form.value.code.trim().toUpperCase(), name: form.value.name.trim(), base: form.value.base, is_default: form.value.is_default, is_active: form.value.is_active,
    deposits: deposits.value.map((d, i) => ({ seq: i + 1, label: d.label.trim(), days_before: d.days_before === null || (d.days_before as unknown) === '' ? null : Number(d.days_before), percent: d.percent.trim() || '0' })),
    tiers: tierInputs(tiers.value),
  }
  try {
    if (props.template) await api.PATCH('/api/v1/properties/{propertyId}/banquet/policy-templates/{tid}', { params: { path: { propertyId: props.propertyId, tid: props.template.id } }, body })
    else await api.POST('/api/v1/properties/{propertyId}/banquet/policy-templates', { params: { path: { propertyId: props.propertyId } }, body })
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
    <DialogContent class="max-h-[90dvh] max-w-3xl overflow-y-auto p-5" data-testid="template-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="save">
        <DialogTitle>{{ template ? t('banquet.template.edit') : t('banquet.template.new') }}</DialogTitle>
        <DialogDescription>{{ t('banquet.template.hint') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <div class="grid gap-3 sm:grid-cols-3">
          <FormField :label="t('banquet.rooms.code')" required :error="error?.fieldMessage('code')"><template #default="{ id }"><Input :id="id" v-model="form.code" name="code" maxlength="20" :disabled="template !== null" class="uppercase" /></template></FormField>
          <FormField :label="t('banquet.rooms.name')" required :error="error?.fieldMessage('name')"><template #default="{ id }"><Input :id="id" v-model="form.name" name="name" maxlength="100" /></template></FormField>
          <FormField :label="t('banquet.policy.base')"><template #default="{ id }"><NativeSelect :id="id" v-model="form.base" name="base"><option value="NET">{{ t('banquet.policy.baseNET') }}</option><option value="GROSS">{{ t('banquet.policy.baseGROSS') }}</option></NativeSelect></template></FormField>
        </div>
        <div class="flex flex-wrap gap-4 text-sm">
          <label class="flex min-h-11 cursor-pointer items-center gap-2"><input v-model="form.is_default" type="checkbox" name="is_default" class="size-4 accent-primary" />{{ t('banquet.template.isDefault') }}</label>
          <label class="flex min-h-11 cursor-pointer items-center gap-2"><input v-model="form.is_active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('common.active') }}</label>
        </div>
        <h3 class="m-0 text-sm font-semibold">{{ t('banquet.template.deposits') }}</h3>
        <div v-for="(d, i) in deposits" :key="i" class="grid grid-cols-[1.6fr_1fr_1fr_auto] items-end gap-2" :data-testid="`template-deposit-${i}`">
          <label class="text-xs text-muted-foreground">{{ t('banquet.deposit.stage') }}<Input v-model="d.label" :name="`dep_label_${i}`" maxlength="100" /></label>
          <label class="text-xs text-muted-foreground">{{ t('banquet.template.daysBefore') }}<Input v-model.number="d.days_before" type="number" min="0" :name="`dep_days_${i}`" :placeholder="t('banquet.template.atSignature')" /></label>
          <label class="text-xs text-muted-foreground">{{ t('banquet.deposit.percent') }}<Input v-model="d.percent" inputmode="decimal" :name="`dep_percent_${i}`" /></label>
          <Button type="button" variant="outline" size="sm" @click="removeDeposit(i)">{{ t('common.remove') }}</Button>
        </div>
        <div><Button type="button" variant="outline" size="sm" data-testid="template-deposit-add" @click="addDeposit">{{ t('banquet.deposit.addRow') }}</Button></div>
        <h3 class="m-0 text-sm font-semibold">{{ t('banquet.template.tiers') }}</h3>
        <BanquetTiersEditor v-model="tiers" />
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy" data-testid="template-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
