<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, TaxFilingProfile } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

interface TaxRow {
  id: number
  code: string
  name: string
  rate: string
  is_active: boolean
  tax_kind?: string
}

const profiles = ref<TaxFilingProfile[]>([])
const taxes = ref<TaxRow[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const editing = ref<TaxFilingProfile | 'new' | null>(null)
const form = reactive({ tax_id: 0, authority: '', registration_number: '', due_day: '15', is_active: true, claims_input_vat: false })
/** The opening VAT credit: set (as of a date, an amount) or void (a reason), each with an approval. */
const credit = ref<{ mode: 'set' | 'void'; profile: TaxFilingProfile; as_of: string; amount: string; reason: string; asking: boolean } | null>(null)
const dialogError = ref<ApiError | null>(null)
const isVat = computed(() => {
  if (editing.value === null) return false
  if (editing.value === 'new') return taxes.value.find((x) => x.id === form.tax_id)?.tax_kind === 'VAT'
  return editing.value.tax_kind === 'VAT'
})

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const columns = computed<Column<TaxFilingProfile>[]>(() => [
  { key: 'tax', label: t('taxProfiles.tax') },
  { key: 'authority', label: t('taxProfiles.authority') },
  { key: 'registration_number', label: t('taxProfiles.registration') },
  { key: 'due_day', label: t('taxProfiles.due') },
  { key: 'vat', label: t('taxProfiles.inputVat') },
  { key: 'status', label: t('taxProfiles.status') },
  { key: 'actions', label: '', align: 'right' },
])
const choices = computed(() => {
  const taken = new Set(profiles.value.map((p) => p.tax_id))
  return taxes.value.filter((x) => !taken.has(x.id))
})

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('tax.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/profiles', { params: { path: { propertyId } } })
    profiles.value = data?.data ?? []
    if (can('tax.manage') && !taxes.value.length) {
      const res = await api.GET('/api/v1/properties/{propertyId}/taxes', { params: { path: { propertyId } } })
      taxes.value = ((res.data as { data?: TaxRow[] } | undefined)?.data ?? []).filter((x) => x.is_active)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, { tax_id: 0, authority: '', registration_number: '', due_day: '15', is_active: true, claims_input_vat: false })
  error.value = null
  editing.value = 'new'
}

function startEdit(p: TaxFilingProfile): void {
  Object.assign(form, { tax_id: p.tax_id, authority: p.authority, registration_number: p.registration_number ?? '', due_day: String(p.due_day), is_active: p.is_active, claims_input_vat: p.claims_input_vat })
  error.value = null
  editing.value = p
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || editing.value === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/tax/profiles', {
        params: { path: { propertyId } },
        body: { tax_id: form.tax_id, authority: form.authority, registration_number: form.registration_number || undefined, due_day: Number(form.due_day), is_active: form.is_active, claims_input_vat: isVat.value ? form.claims_input_vat : undefined },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/tax/profiles/{id}', {
        params: { path: { propertyId, id: editing.value.id } },
        body: { authority: form.authority, registration_number: form.registration_number, due_day: Number(form.due_day), is_active: form.is_active, claims_input_vat: form.claims_input_vat },
      })
    }
    notice.value = t('taxProfiles.saved')
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

function startCredit(mode: 'set' | 'void', profile: TaxFilingProfile): void {
  error.value = null
  notice.value = ''
  credit.value = { mode, profile, as_of: '', amount: '', reason: '', asking: false }
}

async function confirmCredit(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const c = credit.value
  if (propertyId === null || c === null) return
  busy.value = true
  dialogError.value = null
  try {
    const path = { propertyId, id: c.profile.id }
    if (c.mode === 'set') await api.POST('/api/v1/properties/{propertyId}/tax/profiles/{id}/opening-credit', { params: { path }, body: { as_of: c.as_of, amount: c.amount.trim(), approval } })
    else await api.POST('/api/v1/properties/{propertyId}/tax/profiles/{id}/opening-credit/void', { params: { path }, body: { reason: c.reason.trim(), approval } })
    notice.value = c.mode === 'set' ? t('taxProfiles.creditSet') : t('taxProfiles.creditVoided')
    credit.value = null
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  profiles.value = []
  taxes.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('taxProfiles.title')" :description="pid !== null && can('tax.view') ? t('taxProfiles.intro') : undefined">
    <template #actions>
      <Button v-if="can('tax.manage') && !editing" type="button" data-testid="new-profile" @click="startNew">{{ t('taxProfiles.setUp') }}</Button>
    </template>
  </PageHeader>
  <p v-if="error" class="alert" role="alert" data-testid="profile-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 4)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('tax.view')" class="muted" data-testid="no-access">{{ t('taxProfiles.noAccess', { permission: 'tax.view' }) }}</p>
  <template v-else>
    <Card v-if="editing" class="mb-4">
      <form novalidate data-testid="profile-form" @submit.prevent="save">
        <CardHeader><CardTitle>{{ editing === 'new' ? t('taxProfiles.setUp') : t('taxProfiles.edit', { code: editing.tax_code }) }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <FormField v-if="editing === 'new'" :label="t('taxProfiles.tax')" :error="fieldError('tax_id')">
              <template #default="{ id, invalid }">
                <Combobox :id="id" v-model="form.tax_id" name="tax_id" :aria-invalid="invalid" :options="[{ value: 0, label: `${t('taxProfiles.chooseTax')}` }, ...choices.map((x) => ({ value: x.id, label: `${x.code} · ${x.name} (${Number(x.rate)}%)` }))]" />
              </template>
            </FormField>
            <FormField :label="t('taxProfiles.authority')" :error="fieldError('authority')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.authority" name="authority" maxlength="150" :placeholder="t('taxProfiles.authorityPlaceholder')" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('taxProfiles.registration')">
              <template #default="{ id }"><Input :id="id" v-model="form.registration_number" name="registration_number" maxlength="60" /></template>
            </FormField>
            <FormField :label="t('taxProfiles.dueDay')" :error="fieldError('due_day')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.due_day" name="due_day" inputmode="numeric" :aria-invalid="invalid" /></template>
            </FormField>
            <label class="flex items-center gap-2 self-end pb-2 text-sm">
              <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" /><span>{{ t('taxProfiles.filedCheck') }}</span>
            </label>
            <label v-if="isVat" class="flex items-center gap-2 self-end pb-2 text-sm" :title="t('taxProfiles.claimsHint')">
              <input v-model="form.claims_input_vat" name="claims_input_vat" type="checkbox" class="size-4 accent-primary" /><span>{{ t('taxProfiles.claimsInputVat') }}</span>
            </label>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || (editing === 'new' && !form.tax_id) || !form.authority.trim()">{{ t('common.save') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>
    <Card v-if="credit && !credit.asking" class="mb-4">
      <form novalidate data-testid="credit-form" @submit.prevent="credit.asking = true">
        <CardHeader><CardTitle>{{ credit.mode === 'set' ? t('taxProfiles.creditSetTitle', { code: credit.profile.tax_code }) : t('taxProfiles.creditVoidTitle', { code: credit.profile.tax_code }) }}</CardTitle></CardHeader>
        <CardContent>
          <p class="mt-0 text-sm text-muted-foreground">{{ credit.mode === 'set' ? t('taxProfiles.creditSetHint') : t('taxProfiles.creditVoidHint') }}</p>
          <div class="grid gap-4 sm:grid-cols-2">
            <template v-if="credit.mode === 'set'">
              <FormField :label="t('taxProfiles.creditAsOf')" :error="fieldError('as_of')">
                <template #default="{ id, invalid }"><Input :id="id" v-model="credit.as_of" name="as_of" type="date" :aria-invalid="invalid" /></template>
              </FormField>
              <FormField :label="t('taxProfiles.creditAmount')" :error="fieldError('amount')">
                <template #default="{ id, invalid }"><Input :id="id" v-model="credit.amount" name="amount" inputmode="decimal" :aria-invalid="invalid" /></template>
              </FormField>
            </template>
            <FormField v-else :label="t('taxProfiles.creditReason')">
              <template #default="{ id }"><Input :id="id" v-model="credit.reason" name="reason" maxlength="500" /></template>
            </FormField>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="credit = null">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || (credit.mode === 'set' ? !credit.as_of || !credit.amount.trim() : !credit.reason.trim())" data-testid="credit-ask">{{ t('taxProfiles.creditContinue') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>
    <Card>
      <EmptyState v-if="loaded && !profiles.length" :title="t('taxProfiles.empty')" data-testid="empty" />
      <DataTable v-else :columns="columns" :rows="profiles" row-key="id" :row-test-id="(p) => `profile-${p.tax_code}`" :row-class="(p) => (p.is_active ? undefined : 'text-muted-foreground')" :caption="t('taxProfiles.title')" data-testid="profiles">
        <template #cell-tax="{ row }"><b>{{ row.tax_code }}</b> · {{ row.tax_name }} <small class="text-muted-foreground">{{ Number(row.tax_rate) }}%</small></template>
        <template #cell-registration_number="{ row }">{{ row.registration_number ?? '—' }}</template>
        <template #cell-due_day="{ row }">{{ t('taxProfiles.dayN', { n: row.due_day }) }}</template>
        <template #cell-vat="{ row }">
          <Badge v-if="row.claims_input_vat" variant="success" data-testid="claims">{{ t('taxProfiles.claims') }}</Badge>
          <small v-if="Number(row.opening_credit)" class="block text-muted-foreground" data-testid="opening-credit">{{ t('taxProfiles.openingCredit', { amount: $money(row.opening_credit) }) }}</small>
        </template>
        <template #cell-status="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('taxProfiles.filed') : t('taxProfiles.notFiled') }}</Badge></template>
        <template #cell-actions="{ row }">
          <div class="flex items-center justify-end gap-2">
            <RouterLink :to="{ path: '/tax/returns', query: { tax: String(row.tax_id) } }" class="text-sm text-primary hover:underline">{{ t('taxProfiles.returns') }}</RouterLink>
            <Button v-if="can('tax.manage') && row.claims_input_vat && !Number(row.opening_credit)" type="button" variant="outline" size="sm" :data-testid="`credit-${row.tax_code}`" @click="startCredit('set', row)">{{ t('taxProfiles.setOpeningCredit') }}</Button>
            <Button v-if="can('tax.manage') && Number(row.opening_credit)" type="button" variant="outline" size="sm" :data-testid="`void-credit-${row.tax_code}`" @click="startCredit('void', row)">{{ t('taxProfiles.voidOpeningCredit') }}</Button>
            <Button v-if="can('tax.manage')" type="button" variant="outline" size="sm" :data-testid="`edit-${row.tax_code}`" @click="startEdit(row)">{{ t('common.edit') }}</Button>
          </div>
        </template>
      </DataTable>
    </Card>
  </template>
  <ApprovalDialog v-if="credit?.asking" :title="t('taxProfiles.approveCredit')" :busy="busy" :error="dialogError" @approve="confirmCredit" @cancel="credit = null; dialogError = null" />
</template>
