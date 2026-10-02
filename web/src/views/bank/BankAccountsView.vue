<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BankAccount, GlAccount } from '@/api/types'
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
    notice.value = 'Bank account saved.'
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
  <div class="page-head">
    <h1 class="page-title">Bank accounts</h1>
    <button v-if="can('bank.manage') && !editing" type="button" class="btn-primary" data-testid="new-bank" @click="startNew">Register an account</button>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="bank-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 4)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('bank.view')" class="muted" data-testid="no-access">Your role at this property cannot see bank accounts: the <code>bank.view</code> permission is needed.</p>
  <template v-else>
    <p class="muted">The accounts of the books that are reconciled with a bank statement. Register the cash and bank accounts here, then import a statement for each period.</p>
    <form v-if="editing" class="card" novalidate data-testid="bank-form" @submit.prevent="save">
      <h2>{{ editing === 'new' ? 'Register an account' : `Edit ${editing.name}` }}</h2>
      <div class="form-grid">
        <label v-if="editing === 'new'" class="field">
          <span>Account of the books</span>
          <select v-model.number="form.account_id" name="account_id" :aria-invalid="!!fieldError('account_id')">
            <option :value="0">Choose an account</option>
            <option v-for="a in choices" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
          </select>
          <small v-if="fieldError('account_id')" class="error-text">{{ fieldError('account_id') }}</small>
        </label>
        <label class="field">
          <span>Name</span>
          <input v-model="form.name" name="name" maxlength="100" :aria-invalid="!!fieldError('name')" />
          <small v-if="fieldError('name')" class="error-text">{{ fieldError('name') }}</small>
        </label>
        <label class="field"><span>Account number at the bank</span><input v-model="form.account_number" name="account_number" maxlength="60" /></label>
        <label class="check"><input v-model="form.is_active" name="is_active" type="checkbox" /><span>In use</span></label>
      </div>
      <div class="form-actions">
        <button type="button" @click="editing = null">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy || (editing === 'new' && !form.account_id) || !form.name.trim()">Save</button>
      </div>
    </form>
    <section class="card">
      <p v-if="loaded && !banks.length" class="muted" data-testid="empty">No bank account is registered yet.</p>
      <table v-else class="list" data-testid="banks">
        <thead><tr><th>Name</th><th>Account of the books</th><th class="num">Book balance</th><th>Reconciled to</th><th>Open statements</th><th /></tr></thead>
        <tbody>
          <tr v-for="b in banks" :key="b.id" :class="{ off: !b.is_active }" :data-testid="`bank-${b.account_code}`">
            <td><b>{{ b.name }}</b><small v-if="b.account_number" class="muted"> · {{ b.account_number }}</small><small v-if="!b.is_active" class="muted"> · not in use</small></td>
            <td>{{ b.account_code }} - {{ b.account_name }}</td>
            <td class="num">{{ b.book_balance }}</td>
            <td>{{ b.reconciled_to ?? 'never' }}</td>
            <td>{{ b.open_statements }}</td>
            <td class="row-actions">
              <RouterLink :to="{ path: '/bank/statements', query: { bank: String(b.id) } }">Statements</RouterLink>
              <button v-if="can('bank.manage')" type="button" :data-testid="`edit-${b.account_code}`" @click="startEdit(b)">Edit</button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </template>
</template>

<style scoped>
.num {
  text-align: right;
  white-space: nowrap;
}
.off td {
  color: var(--muted, #6b7280);
}
.row-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}
</style>
