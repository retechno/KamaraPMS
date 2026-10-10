<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, TaxSettings } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const current = ref<TaxSettings | null>(null)
const history = ref<TaxSettings[]>([])
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const editing = ref(false)
const asking = ref(false)
const blank = () => ({ effective_from: '', is_pkp: false, npwp: '', pkp_number: '', pkp_confirmed_on: '', input_vat_treatment: 'EXPENSE', signer_name: '', signer_title: '' })
const form = reactive(blank())

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const treatments = ['CREDITABLE', 'EXPENSE', 'DEFERRED'] as const
const options = computed(() => treatments.filter((x) => form.is_pkp || x !== 'CREDITABLE').map((x) => ({ value: x, label: t('taxStatus.treatment_' + x) })))
// Turning PKP on or off moves the treatment to its usual choice; a property that is not PKP cannot claim input VAT.
watch(() => form.is_pkp, (pkp) => { form.input_vat_treatment = pkp ? 'CREDITABLE' : 'EXPENSE' })
const columns = computed<Column<TaxSettings>[]>(() => [
  { key: 'from', label: t('taxStatus.from') },
  { key: 'status', label: t('taxStatus.status') },
  { key: 'npwp', label: t('taxStatus.npwp') },
  { key: 'treatment', label: t('taxStatus.treatment') },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('tax.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/settings', { params: { path: { propertyId } } })
    current.value = data?.current ?? null
    history.value = data?.history ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function startChange(): void {
  Object.assign(form, blank(), {
    is_pkp: current.value?.is_pkp ?? false,
    input_vat_treatment: current.value?.input_vat_treatment ?? 'EXPENSE',
    npwp: current.value?.npwp ?? '',
    pkp_number: current.value?.pkp_number ?? '',
    signer_name: current.value?.signer_name ?? '',
    signer_title: current.value?.signer_title ?? '',
  })
  error.value = null
  editing.value = true
}

async function submit(approval?: Approval): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  dialogError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/tax/settings', {
      params: { path: { propertyId } },
      body: {
        effective_from: form.effective_from,
        is_pkp: form.is_pkp,
        npwp: form.npwp.trim() || undefined,
        pkp_number: form.pkp_number.trim() || undefined,
        pkp_confirmed_on: form.pkp_confirmed_on || undefined,
        input_vat_treatment: form.input_vat_treatment as 'EXPENSE',
        signer_name: form.signer_name.trim() || undefined,
        signer_title: form.signer_title.trim() || undefined,
        approval,
      },
    })
    notice.value = t('taxStatus.saved')
    editing.value = false
    asking.value = false
    await load()
  } catch (e) {
    const err = e instanceof ApiError ? e : null
    if (err?.code === 'APPROVAL_REQUIRED' && !approval) {
      asking.value = true
    } else if (asking.value) {
      dialogError.value = err
    } else {
      error.value = err
    }
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  current.value = null
  history.value = []
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('taxStatus.title')" :description="pid !== null && can('tax.view') ? t('taxStatus.intro') : undefined">
    <template #actions>
      <Button v-if="can('tax.manage') && !editing" type="button" data-testid="change-status" @click="startChange">{{ t('taxStatus.change') }}</Button>
    </template>
  </PageHeader>
  <p v-if="error" class="alert" role="alert" data-testid="status-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 4)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('tax.view')" class="muted" data-testid="no-access">{{ t('taxStatus.noAccess', { permission: 'tax.view' }) }}</p>
  <template v-else>
    <Card v-if="current" class="mb-4" data-testid="current">
      <CardHeader><CardTitle>{{ t('taxStatus.current') }}</CardTitle></CardHeader>
      <CardContent>
        <p>
          <Badge :variant="current.is_pkp ? 'success' : 'outline'" data-testid="current-status">{{ current.is_pkp ? t('taxStatus.pkp') : t('taxStatus.notPkp') }}</Badge>
          <span class="ml-2" data-testid="current-treatment">{{ t('taxStatus.treatment') }}: {{ t('taxStatus.treatment_' + current.input_vat_treatment) }}</span>
        </p>
      </CardContent>
    </Card>

    <Card v-if="editing" class="mb-4">
      <form novalidate data-testid="status-form" @submit.prevent="submit()">
        <CardHeader><CardTitle>{{ t('taxStatus.change') }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <FormField :label="t('taxStatus.effectiveFrom')" :error="fieldError('effective_from')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.effective_from" name="effective_from" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <label class="flex items-center gap-2 self-end pb-2 text-sm">
              <input v-model="form.is_pkp" name="is_pkp" type="checkbox" class="size-4 accent-primary" /><span>{{ t('taxStatus.isPkp') }}</span>
            </label>
            <FormField :label="t('taxStatus.treatment')" :hint="t('taxStatus.treatmentHint')" :error="fieldError('input_vat_treatment')">
              <template #default="{ id, invalid }"><Combobox :id="id" v-model="form.input_vat_treatment" name="input_vat_treatment" :aria-invalid="invalid" :options="options" /></template>
            </FormField>
            <FormField :label="t('taxStatus.npwp')" :error="fieldError('npwp')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.npwp" name="npwp" maxlength="30" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('taxStatus.pkpNumber')" :error="fieldError('pkp_number')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.pkp_number" name="pkp_number" maxlength="40" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('taxStatus.confirmedOn')" :error="fieldError('pkp_confirmed_on')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.pkp_confirmed_on" name="pkp_confirmed_on" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('taxStatus.signerName')" :error="fieldError('signer_name')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.signer_name" name="signer_name" maxlength="150" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('taxStatus.signerTitle')" :error="fieldError('signer_title')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.signer_title" name="signer_title" maxlength="100" :aria-invalid="invalid" /></template>
            </FormField>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="editing = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || !form.effective_from">{{ t('taxStatus.save') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card>
      <CardHeader><CardTitle>{{ t('taxStatus.history') }}</CardTitle></CardHeader>
      <DataTable :columns="columns" :rows="history" row-key="id" :row-test-id="(h) => `status-${h.effective_from}`" :caption="t('taxStatus.history')" data-testid="history">
        <template #cell-from="{ row }">{{ $date(row.effective_from) }}</template>
        <template #cell-status="{ row }"><Badge :variant="row.is_pkp ? 'success' : 'outline'">{{ row.is_pkp ? t('taxStatus.pkp') : t('taxStatus.notPkp') }}</Badge></template>
        <template #cell-npwp="{ row }">{{ row.npwp ?? '—' }}</template>
        <template #cell-treatment="{ row }">{{ t('taxStatus.treatment_' + row.input_vat_treatment) }}</template>
      </DataTable>
    </Card>
  </template>
  <ApprovalDialog v-if="asking" :title="t('taxStatus.approveTitle')" :message="t('taxStatus.backdated')" :busy="busy" :error="dialogError" @approve="submit" @cancel="asking = false; dialogError = null" />
</template>
