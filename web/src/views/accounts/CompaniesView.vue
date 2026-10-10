<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Company } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const companies = ref<Company[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<Company | 'new' | null>(null)

const canManage = computed(() => auth.can('company.manage', property.currentId))
const canRead = computed(() => auth.can('reservation.read', property.currentId) || auth.can('cityledger.read', property.currentId))
const blank = () => ({
  code: '', name: '', contact_name: '', email: '', phone: '', address: '', city: '', tax_id: '', credit_limit: '', unlimited: true,
  payment_terms_days: 30, notes: '', is_active: true,
})
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function load(): Promise<void> {
  const propertyId = property.currentId
  companies.value = []
  loaded.value = false
  if (propertyId === null || !canRead.value) return
  try {
    companies.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/companies', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, blank())
  error.value = null
  editing.value = 'new'
}

function startEdit(c: Company): void {
  Object.assign(form, blank(), {
    code: c.code, name: c.name, contact_name: c.contact_name ?? '', email: c.email ?? '', phone: c.phone ?? '', address: c.address ?? '', city: c.city ?? '',
    tax_id: c.tax_id ?? '', credit_limit: c.credit_limit ?? '', unlimited: c.credit_limit === null, payment_terms_days: c.payment_terms_days,
    notes: c.notes ?? '', is_active: c.is_active,
  })
  error.value = null
  editing.value = c
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  const common = {
    name: form.name, contact_name: form.contact_name, email: form.email, phone: form.phone, address: form.address, city: form.city, tax_id: form.tax_id,
    credit_limit: form.unlimited ? '' : form.credit_limit, payment_terms_days: Number(form.payment_terms_days), notes: form.notes, is_active: form.is_active,
  }
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/companies', { params: { path: { propertyId } }, body: { code: form.code, ...common } })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/companies/{id}', { params: { path: { propertyId, id: editing.value.id } }, body: common })
    }
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

const limitText = (c: Company) => (c.credit_limit === null ? t('companies.noLimit') : c.credit_limit === '0' ? t('companies.noCredit') : c.credit_limit)
const columns = computed<Column<Company>[]>(() => [
  { key: 'code', label: t('companies.code'), sortable: true, filter: 'text' as const },
  { key: 'name', label: t('companies.name'), sortable: true, filter: 'text' as const },
  { key: 'contact', label: t('companies.colContact') },
  { key: 'credit', label: t('companies.colCredit') },
  { key: 'terms', label: t('companies.colTerms') },
  { key: 'status', label: t('setup.status'), filter: 'select' as const, filterValue: (c: Company) => (c.is_active ? t('setup.active') : t('setup.inactive')) },
  ...(canManage.value ? [{ key: 'actions', label: '', align: 'right' as const }] : []),
])

watch(() => property.currentId, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('companies.title')">
    <template #actions>
      <Button v-if="canManage && !editing" type="button" data-testid="new-company" @click="startNew">{{ t('companies.new') }}</Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error">
<span v-if="error.code === 'COMPANY_HAS_BALANCE'"> {{ t('companies.hasBalance') }}</span>
</ErrorNotice>
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('companies.noAccess') }}</p>
  <p v-else-if="!canManage" class="muted" data-testid="read-only">{{ t('companies.readOnly', { permission: 'company.manage' }) }}</p>

  <Card v-if="editing" class="mb-4">
    <form v-autofocus="editing" novalidate data-testid="company-form" @submit.prevent="save">
      <CardHeader><CardTitle>{{ editing === 'new' ? t('companies.new') : t('companies.edit', { code: form.code }) }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <FormField :label="t('companies.code')" :error="fieldError('code')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('companies.name')" :error="fieldError('name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('companies.contact')">
            <template #default="{ id }"><Input :id="id" v-model="form.contact_name" name="contact_name" /></template>
          </FormField>
          <FormField :label="t('companies.email')" :error="fieldError('email')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.email" name="email" type="email" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('companies.phone')">
            <template #default="{ id }"><Input :id="id" v-model="form.phone" name="phone" /></template>
          </FormField>
          <FormField :label="t('companies.taxId')">
            <template #default="{ id }"><Input :id="id" v-model="form.tax_id" name="tax_id" /></template>
          </FormField>
          <FormField :label="t('companies.address')">
            <template #default="{ id }"><Input :id="id" v-model="form.address" name="address" /></template>
          </FormField>
          <FormField :label="t('companies.city')">
            <template #default="{ id }"><Input :id="id" v-model="form.city" name="city" /></template>
          </FormField>
          <FormField :label="t('companies.terms')" :error="fieldError('payment_terms_days')">
            <template #default="{ id, invalid }"><Input :id="id" v-model.number="form.payment_terms_days" name="payment_terms_days" type="number" min="0" max="365" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('companies.creditLimit')" :hint="t('companies.creditHint')" :error="fieldError('credit_limit')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.credit_limit" name="credit_limit" inputmode="decimal" :disabled="form.unlimited" :aria-invalid="invalid" /></template>
          </FormField>
          <label class="flex items-center gap-2 self-end pb-2 text-sm">
            <input v-model="form.unlimited" name="unlimited" type="checkbox" class="size-4 accent-primary" />
            <span>{{ t('companies.unlimited') }}</span>
          </label>
          <FormField :label="t('companies.notes')">
            <template #default="{ id }"><Input :id="id" v-model="form.notes" name="notes" /></template>
          </FormField>
          <label class="flex items-center gap-2 self-end pb-2 text-sm">
            <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" />
            <span>{{ t('companies.activeCheck') }}</span>
          </label>
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="saving">{{ t('common.save') }}</Button>
        </div>
      </CardContent>
    </form>
  </Card>

  <Card v-if="canRead">
    <EmptyState v-if="loaded && !companies.length" :title="t('companies.empty')" data-testid="empty" />
    <DataTable v-else-if="companies.length" :columns="columns" :rows="companies" row-key="id" :row-test-id="(c) => `company-${c.code}`" :caption="t('companies.title')">
      <template #cell-code="{ row }"><b>{{ row.code }}</b></template>
      <template #cell-contact="{ row }">{{ row.contact_name }} <small class="text-muted-foreground">{{ row.email }}</small></template>
      <template #cell-credit="{ row }">{{ limitText(row) }}</template>
      <template #cell-terms="{ row }">{{ t('companies.days', { n: row.payment_terms_days }) }}</template>
      <template #cell-status="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('setup.active') : t('setup.inactive') }}</Badge></template>
      <template #cell-actions="{ row }"><Button type="button" variant="outline" size="sm" @click="startEdit(row)">{{ t('common.edit') }}</Button></template>
    </DataTable>
  </Card>
</template>
