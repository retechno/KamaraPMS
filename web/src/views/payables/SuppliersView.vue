<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GlAccount, Supplier } from '@/api/types'
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
    notice.value = 'Supplier saved.'
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
  <div class="page-head">
    <h1 class="page-title">Suppliers</h1>
    <button v-if="can('payables.manage') && !editing" type="button" class="btn-primary" data-testid="new-supplier" @click="startNew">New supplier</button>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="supplier-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 6)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('payables.view')" class="muted" data-testid="no-access">Your role at this property cannot see suppliers: the <code>payables.view</code> permission is needed.</p>
  <template v-else>
    <form v-if="editing" class="card" novalidate data-testid="supplier-form" @submit.prevent="save">
      <h2>{{ editing === 'new' ? 'New supplier' : `Edit ${form.code}` }}</h2>
      <div class="form-grid">
        <label class="field">
          <span>Code</span>
          <input v-model="form.code" name="code" maxlength="20" :disabled="editing !== 'new'" :aria-invalid="!!fieldError('code')" />
          <small v-if="fieldError('code')" class="error-text">{{ fieldError('code') }}</small>
        </label>
        <label class="field">
          <span>Name</span>
          <input v-model="form.name" name="name" maxlength="150" :aria-invalid="!!fieldError('name')" />
          <small v-if="fieldError('name')" class="error-text">{{ fieldError('name') }}</small>
        </label>
        <label class="field"><span>Contact</span><input v-model="form.contact_name" name="contact_name" maxlength="150" /></label>
        <label class="field">
          <span>E-mail</span>
          <input v-model="form.email" name="email" type="email" maxlength="254" :aria-invalid="!!fieldError('email')" />
          <small v-if="fieldError('email')" class="error-text">{{ fieldError('email') }}</small>
        </label>
        <label class="field"><span>Phone</span><input v-model="form.phone" name="phone" maxlength="40" /></label>
        <label class="field"><span>Tax ID</span><input v-model="form.tax_id" name="tax_id" maxlength="40" /></label>
        <label class="field"><span>Address</span><input v-model="form.address" name="address" maxlength="300" /></label>
        <label class="field"><span>City</span><input v-model="form.city" name="city" maxlength="100" /></label>
        <label class="field">
          <span>Payment terms (days)</span>
          <input v-model="form.payment_terms_days" name="payment_terms_days" inputmode="numeric" :aria-invalid="!!fieldError('payment_terms_days')" />
          <small v-if="fieldError('payment_terms_days')" class="error-text">{{ fieldError('payment_terms_days') }}</small>
        </label>
        <label v-if="accounts.length" class="field">
          <span>Usual expense account</span>
          <select v-model.number="form.default_account_id" name="default_account_id" :aria-invalid="!!fieldError('default_account_id')">
            <option :value="0">None</option>
            <option v-for="a in expenseAccounts" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
          </select>
          <small v-if="fieldError('default_account_id')" class="error-text">{{ fieldError('default_account_id') }}</small>
        </label>
        <label class="field wide"><span>Bank details</span><input v-model="form.bank_details" name="bank_details" maxlength="300" /></label>
        <label class="field wide"><span>Notes</span><input v-model="form.notes" name="notes" maxlength="1000" /></label>
        <label class="check"><input v-model="form.is_active" name="is_active" type="checkbox" /><span>Active (takes bills)</span></label>
      </div>
      <div class="form-actions">
        <button type="button" @click="editing = null">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy">Save</button>
      </div>
    </form>

    <section class="card">
      <form class="filters" novalidate @submit.prevent>
        <label class="field"><span>Search</span><input v-model="filter.q" name="q" type="search" placeholder="Code or name" /></label>
        <label class="check"><input v-model="filter.inactive" name="inactive" type="checkbox" /><span>Show inactive</span></label>
      </form>
      <p v-if="loaded && !visible.length" class="muted" data-testid="empty">No suppliers.</p>
      <table v-else class="list" data-testid="suppliers">
        <thead><tr><th>Code</th><th>Name</th><th>Terms</th><th>Usual account</th><th class="num">Owed</th><th /></tr></thead>
        <tbody>
          <tr v-for="s in visible" :key="s.id" :class="{ off: !s.is_active }" :data-testid="`supplier-${s.code}`">
            <td><b>{{ s.code }}</b></td>
            <td>{{ s.name }}<small v-if="!s.is_active" class="muted"> · inactive</small></td>
            <td>{{ s.payment_terms_days }} days</td>
            <td>{{ s.default_account_code ? `${s.default_account_code} - ${s.default_account_name}` : '—' }}</td>
            <td class="num">{{ s.outstanding }}</td>
            <td class="row-actions">
              <RouterLink :to="{ path: '/payables/bills', query: { supplier: String(s.id) } }">Bills</RouterLink>
              <button v-if="can('payables.manage')" type="button" :data-testid="`edit-${s.code}`" @click="startEdit(s)">Edit</button>
            </td>
          </tr>
        </tbody>
        <tfoot><tr><th colspan="4">Total owed</th><th class="num" data-testid="owed">{{ owed }}</th><th /></tr></tfoot>
      </table>
    </section>
  </template>
</template>

<style scoped>
.wide {
  grid-column: 1 / -1;
}
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
