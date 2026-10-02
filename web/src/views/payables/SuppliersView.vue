<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GlAccount, Supplier } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { listAccounts } from '@/views/accounting/accountApi'

const auth = useAuthStore()
const property = usePropertyStore()

const suppliers = ref<Supplier[]>([])
const accounts = ref<GlAccount[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const editing = ref<Supplier | 'new' | null>(null)
const filter = reactive({ q: '', inactive: false })
const form = reactive({ code: '', name: '', contact_name: '', email: '', phone: '', address: '', city: '', tax_id: '', payment_terms_days: '30', default_account_id: 0, bank_details: '', notes: '', is_active: true })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const expenseAccounts = computed(() => accounts.value.filter((a) => a.is_postable && a.is_active && a.account_type !== 'REVENUE'))
const visible = computed(() => {
  const q = filter.q.trim().toLowerCase()
  return suppliers.value.filter((s) => (filter.inactive || s.is_active) && (!q || s.code.toLowerCase().includes(q) || s.name.toLowerCase().includes(q)))
})
const columns = computed<Column<Supplier>[]>(() => [
  { key: 'code', label: t('payables.code') },
  { key: 'name', label: t('payables.name') },
  { key: 'terms', label: t('payables.colTerms') },
  { key: 'account', label: t('payables.usualAccount') },
  { key: 'outstanding', label: t('payables.owed'), align: 'right', format: 'money' as const },
  { key: 'actions', label: '', align: 'right' },
])
const owed = computed(() => visible.value.reduce((sum, s) => sum + Number(s.outstanding), 0))

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('payables.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/suppliers', { params: { path: { propertyId } } })
    suppliers.value = data?.data ?? []
    if (can('accounting.view') && !accounts.value.length) accounts.value = await listAccounts(propertyId, { active: true })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, { code: '', name: '', contact_name: '', email: '', phone: '', address: '', city: '', tax_id: '', payment_terms_days: '30', default_account_id: 0, bank_details: '', notes: '', is_active: true })
  error.value = null
  editing.value = 'new'
}

function startEdit(s: Supplier): void {
  Object.assign(form, {
    code: s.code, name: s.name, contact_name: s.contact_name ?? '', email: s.email ?? '', phone: s.phone ?? '', address: s.address ?? '', city: s.city ?? '', tax_id: s.tax_id ?? '',
    payment_terms_days: String(s.payment_terms_days), default_account_id: s.default_account_id ?? 0, bank_details: s.bank_details ?? '', notes: s.notes ?? '', is_active: s.is_active,
  })
  error.value = null
  editing.value = s
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || editing.value === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  const terms = Number(form.payment_terms_days)
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/payables/suppliers', {
        params: { path: { propertyId } },
        body: {
          code: form.code, name: form.name, contact_name: form.contact_name || undefined, email: form.email || undefined, phone: form.phone || undefined, address: form.address || undefined,
          city: form.city || undefined, tax_id: form.tax_id || undefined, payment_terms_days: terms, default_account_id: form.default_account_id || null,
          bank_details: form.bank_details || undefined, notes: form.notes || undefined, is_active: form.is_active,
        },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/payables/suppliers/{id}', {
        params: { path: { propertyId, id: editing.value.id } },
        body: {
          name: form.name, contact_name: form.contact_name, email: form.email, phone: form.phone, address: form.address, city: form.city, tax_id: form.tax_id, payment_terms_days: terms,
          default_account_id: form.default_account_id || 0, bank_details: form.bank_details, notes: form.notes, is_active: form.is_active,
        },
      })
    }
    notice.value = t('payables.sSaved')
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  suppliers.value = []
  accounts.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('payables.sTitle')">
    <template #actions>
      <Button v-if="can('payables.manage') && !editing" type="button" data-testid="new-supplier" @click="startNew">{{ t('payables.sNew') }}</Button>
    </template>
  </PageHeader>
  <p v-if="error" class="alert" role="alert" data-testid="supplier-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 6)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('payables.view')" class="muted" data-testid="no-access">{{ t('payables.sNoAccess', { permission: 'payables.view' }) }}</p>
  <template v-else>
    <Card v-if="editing" class="mb-4">
      <form novalidate data-testid="supplier-form" @submit.prevent="save">
        <CardHeader><CardTitle>{{ editing === 'new' ? t('payables.sNew') : t('payables.sEdit', { code: form.code }) }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <FormField :label="t('payables.code')" :error="fieldError('code')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" maxlength="20" :disabled="editing !== 'new'" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('payables.name')" :error="fieldError('name')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" maxlength="150" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('payables.contact')">
              <template #default="{ id }"><Input :id="id" v-model="form.contact_name" name="contact_name" maxlength="150" /></template>
            </FormField>
            <FormField :label="t('payables.email')" :error="fieldError('email')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.email" name="email" type="email" maxlength="254" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('payables.phone')">
              <template #default="{ id }"><Input :id="id" v-model="form.phone" name="phone" maxlength="40" /></template>
            </FormField>
            <FormField :label="t('payables.taxId')">
              <template #default="{ id }"><Input :id="id" v-model="form.tax_id" name="tax_id" maxlength="40" /></template>
            </FormField>
            <FormField :label="t('payables.address')">
              <template #default="{ id }"><Input :id="id" v-model="form.address" name="address" maxlength="300" /></template>
            </FormField>
            <FormField :label="t('payables.city')">
              <template #default="{ id }"><Input :id="id" v-model="form.city" name="city" maxlength="100" /></template>
            </FormField>
            <FormField :label="t('payables.terms')" :error="fieldError('payment_terms_days')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.payment_terms_days" name="payment_terms_days" inputmode="numeric" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField v-if="accounts.length" :label="t('payables.usualExpense')" :error="fieldError('default_account_id')">
              <template #default="{ id, invalid }">
                <NativeSelect :id="id" v-model.number="form.default_account_id" name="default_account_id" :aria-invalid="invalid">
                  <option :value="0">{{ t('payables.none') }}</option>
                  <option v-for="a in expenseAccounts" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField class="sm:col-span-2 lg:col-span-3" :label="t('payables.bankDetails')">
              <template #default="{ id }"><Input :id="id" v-model="form.bank_details" name="bank_details" maxlength="300" /></template>
            </FormField>
            <FormField class="sm:col-span-2 lg:col-span-3" :label="t('payables.notes')">
              <template #default="{ id }"><Input :id="id" v-model="form.notes" name="notes" maxlength="1000" /></template>
            </FormField>
            <label class="flex items-center gap-2 text-sm">
              <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" /><span>{{ t('payables.activeCheck') }}</span>
            </label>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy">{{ t('common.save') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card>
      <CardContent class="pt-4">
        <form class="mb-4 flex flex-wrap items-end gap-4" novalidate @submit.prevent>
          <FormField class="w-64" :label="t('payables.search')">
            <template #default="{ id }"><Input :id="id" v-model="filter.q" name="q" type="search" :placeholder="t('payables.codeOrName')" /></template>
          </FormField>
          <label class="flex items-center gap-2 pb-2 text-sm">
            <input v-model="filter.inactive" name="inactive" type="checkbox" class="size-4 accent-primary" /><span>{{ t('payables.showInactive') }}</span>
          </label>
        </form>
        <EmptyState v-if="loaded && !visible.length" :title="t('payables.sEmpty')" data-testid="empty" />
        <template v-else>
          <DataTable :columns="columns" :rows="visible" row-key="id" :row-test-id="(s) => `supplier-${s.code}`" :row-class="(s) => (s.is_active ? undefined : 'text-muted-foreground')" :caption="t('payables.sTitle')" data-testid="suppliers">
            <template #cell-code="{ row }"><b>{{ row.code }}</b></template>
            <template #cell-name="{ row }">{{ row.name }}<small v-if="!row.is_active" class="text-muted-foreground"> · {{ t('payables.inactiveNote') }}</small></template>
            <template #cell-terms="{ row }">{{ t('payables.days', { n: row.payment_terms_days }) }}</template>
            <template #cell-account="{ row }">{{ row.default_account_code ? `${row.default_account_code} - ${row.default_account_name}` : '—' }}</template>
            <template #cell-actions="{ row }">
              <div class="flex items-center justify-end gap-2">
                <RouterLink :to="{ path: '/payables/bills', query: { supplier: String(row.id) } }" class="text-sm text-primary hover:underline">{{ t('payables.bills') }}</RouterLink>
                <Button v-if="can('payables.manage')" type="button" variant="outline" size="sm" :data-testid="`edit-${row.code}`" @click="startEdit(row)">{{ t('common.edit') }}</Button>
              </div>
            </template>
          </DataTable>
          <p class="mb-0 mt-3 flex justify-between border-t border-border pt-3 text-sm font-semibold">
            <span>{{ t('payables.totalOwed') }}</span><span class="tabular-nums" data-testid="owed">{{ $money(owed) }}</span>
          </p>
        </template>
      </CardContent>
    </Card>
  </template>
</template>
