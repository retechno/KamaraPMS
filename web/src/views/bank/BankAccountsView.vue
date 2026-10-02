<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BankAccount, GlAccount } from '@/api/types'
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

const banks = ref<BankAccount[]>([])
const accounts = ref<GlAccount[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const editing = ref<BankAccount | 'new' | null>(null)
const form = reactive({ account_id: 0, name: '', account_number: '', is_active: true })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const columns = computed<Column<BankAccount>[]>(() => [
  { key: 'name', label: t('bankAccounts.name') },
  { key: 'account', label: t('bankAccounts.booksAccount') },
  { key: 'book_balance', label: t('bankAccounts.bookBalance'), align: 'right' },
  { key: 'reconciled_to', label: t('bankAccounts.reconciledTo') },
  { key: 'open_statements', label: t('bankAccounts.openStatements') },
  { key: 'actions', label: '', align: 'right' },
])
// Asset accounts that take postings and are not registered yet (cash and bank accounts first).
const choices = computed(() => {
  const taken = new Set(banks.value.map((b) => b.account_id))
  return accounts.value.filter((a) => a.account_type === 'ASSET' && a.is_postable && a.is_active && !taken.has(a.id) && a.statement_group === 'CASH')
})

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('bank.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/bank/accounts', { params: { path: { propertyId } } })
    banks.value = data?.data ?? []
    if (can('accounting.view') && !accounts.value.length) accounts.value = await listAccounts(propertyId, { active: true })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, { account_id: 0, name: '', account_number: '', is_active: true })
  error.value = null
  editing.value = 'new'
}

function startEdit(b: BankAccount): void {
  Object.assign(form, { account_id: b.account_id, name: b.name, account_number: b.account_number ?? '', is_active: b.is_active })
  error.value = null
  editing.value = b
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || editing.value === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/bank/accounts', {
        params: { path: { propertyId } }, body: { account_id: form.account_id, name: form.name, account_number: form.account_number || undefined, is_active: form.is_active },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/bank/accounts/{id}', {
        params: { path: { propertyId, id: editing.value.id } }, body: { name: form.name, account_number: form.account_number, is_active: form.is_active },
      })
    }
    notice.value = t('bankAccounts.saved')
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  banks.value = []
  accounts.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('bankAccounts.title')" :description="pid !== null && can('bank.view') ? t('bankAccounts.intro') : undefined">
    <template #actions>
      <Button v-if="can('bank.manage') && !editing" type="button" data-testid="new-bank" @click="startNew">{{ t('bankAccounts.register') }}</Button>
    </template>
  </PageHeader>
  <p v-if="error" class="alert" role="alert" data-testid="bank-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 4)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('bank.view')" class="muted" data-testid="no-access">{{ t('bankAccounts.noAccess', { permission: 'bank.view' }) }}</p>
  <template v-else>
    <Card v-if="editing" class="mb-4">
      <form novalidate data-testid="bank-form" @submit.prevent="save">
        <CardHeader><CardTitle>{{ editing === 'new' ? t('bankAccounts.register') : t('bankAccounts.edit', { name: editing.name }) }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <FormField v-if="editing === 'new'" :label="t('bankAccounts.booksAccount')" :error="fieldError('account_id')">
              <template #default="{ id, invalid }">
                <NativeSelect :id="id" v-model.number="form.account_id" name="account_id" :aria-invalid="invalid">
                  <option :value="0">{{ t('bankAccounts.chooseAccount') }}</option>
                  <option v-for="a in choices" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('bankAccounts.name')" :error="fieldError('name')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" maxlength="100" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('bankAccounts.accountNumber')">
              <template #default="{ id }"><Input :id="id" v-model="form.account_number" name="account_number" maxlength="60" /></template>
            </FormField>
            <label class="flex items-center gap-2 text-sm">
              <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" /><span>{{ t('bankAccounts.inUse') }}</span>
            </label>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || (editing === 'new' && !form.account_id) || !form.name.trim()">{{ t('common.save') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>
    <Card>
      <EmptyState v-if="loaded && !banks.length" :title="t('bankAccounts.empty')" data-testid="empty" />
      <DataTable v-else :columns="columns" :rows="banks" row-key="id" :row-test-id="(b) => `bank-${b.account_code}`" :row-class="(b) => (b.is_active ? undefined : 'text-muted-foreground')" :caption="t('bankAccounts.title')" data-testid="banks">
        <template #cell-name="{ row }">
          <b>{{ row.name }}</b><small v-if="row.account_number" class="text-muted-foreground"> · {{ row.account_number }}</small><small v-if="!row.is_active" class="text-muted-foreground"> · {{ t('bankAccounts.notInUse') }}</small>
        </template>
        <template #cell-account="{ row }">{{ row.account_code }} - {{ row.account_name }}</template>
        <template #cell-reconciled_to="{ row }">{{ row.reconciled_to ?? t('bankAccounts.never') }}</template>
        <template #cell-actions="{ row }">
          <div class="flex items-center justify-end gap-2">
            <RouterLink :to="{ path: '/bank/statements', query: { bank: String(row.id) } }" class="text-sm text-primary hover:underline">{{ t('bankAccounts.statements') }}</RouterLink>
            <Button v-if="can('bank.manage')" type="button" variant="outline" size="sm" :data-testid="`edit-${row.account_code}`" @click="startEdit(row)">{{ t('common.edit') }}</Button>
          </div>
        </template>
      </DataTable>
    </Card>
  </template>
</template>
