<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Company } from '@/api/types'
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

const limitText = (c: Company) => (c.credit_limit === null ? 'No limit' : c.credit_limit === '0' ? 'No credit' : c.credit_limit)

watch(() => property.currentId, load, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Companies</h1>
    <button v-if="canManage && !editing" type="button" class="btn-primary" @click="startNew">New company</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <span v-if="error.code === 'COMPANY_HAS_BALANCE'"> Record the company's payments under City ledger first.</span>
  </p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property cannot see companies.</p>
  <p v-else-if="!canManage" class="muted" data-testid="read-only">
    Your role can view companies but not edit them: the <code>company.manage</code> permission is needed.
  </p>

  <form v-if="editing" class="card" novalidate data-testid="company-form" @submit.prevent="save">
    <h2>{{ editing === 'new' ? 'New company' : `Edit ${form.code}` }}</h2>
    <div class="form-grid">
      <label class="field">
        <span>Code</span>
        <input v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="!!fieldError('code')" />
        <small v-if="fieldError('code')" class="error-text">{{ fieldError('code') }}</small>
      </label>
      <label class="field">
        <span>Name</span>
        <input v-model="form.name" name="name" :aria-invalid="!!fieldError('name')" />
        <small v-if="fieldError('name')" class="error-text">{{ fieldError('name') }}</small>
      </label>
      <label class="field">
        <span>Contact person</span>
        <input v-model="form.contact_name" name="contact_name" />
      </label>
      <label class="field">
        <span>Email</span>
        <input v-model="form.email" name="email" type="email" :aria-invalid="!!fieldError('email')" />
        <small v-if="fieldError('email')" class="error-text">{{ fieldError('email') }}</small>
      </label>
      <label class="field">
        <span>Phone</span>
        <input v-model="form.phone" name="phone" />
      </label>
      <label class="field">
        <span>Tax ID</span>
        <input v-model="form.tax_id" name="tax_id" />
      </label>
      <label class="field">
        <span>Address</span>
        <input v-model="form.address" name="address" />
      </label>
      <label class="field">
        <span>City</span>
        <input v-model="form.city" name="city" />
      </label>
      <label class="field">
        <span>Payment terms (days)</span>
        <input v-model.number="form.payment_terms_days" name="payment_terms_days" type="number" min="0" max="365" :aria-invalid="!!fieldError('payment_terms_days')" />
      </label>
      <label class="field">
        <span>Credit limit</span>
        <input v-model="form.credit_limit" name="credit_limit" inputmode="decimal" :disabled="form.unlimited" :aria-invalid="!!fieldError('credit_limit')" />
        <small class="hint">0 means no credit: nothing can be transferred to this company.</small>
        <small v-if="fieldError('credit_limit')" class="error-text">{{ fieldError('credit_limit') }}</small>
      </label>
      <label class="check">
        <input v-model="form.unlimited" name="unlimited" type="checkbox" />
        <span>No credit limit</span>
      </label>
      <label class="field">
        <span>Notes</span>
        <input v-model="form.notes" name="notes" />
      </label>
      <label class="check">
        <input v-model="form.is_active" name="is_active" type="checkbox" />
        <span>Active (can be billed)</span>
      </label>
    </div>
    <div class="form-actions">
      <button type="button" @click="editing = null">Cancel</button>
      <button type="submit" class="btn-primary" :disabled="saving">Save</button>
    </div>
  </form>

  <section v-if="canRead" class="card">
    <p v-if="loaded && !companies.length" class="muted" data-testid="empty">No companies yet.</p>
    <table v-else-if="companies.length" class="list">
      <thead>
        <tr>
          <th>Code</th>
          <th>Name</th>
          <th>Contact</th>
          <th>Credit limit</th>
          <th>Terms</th>
          <th>Status</th>
          <th v-if="canManage" />
        </tr>
      </thead>
      <tbody>
        <tr v-for="c in companies" :key="c.id" :data-testid="`company-${c.code}`">
          <td><b>{{ c.code }}</b></td>
          <td>{{ c.name }}</td>
          <td>{{ c.contact_name }} <small class="muted">{{ c.email }}</small></td>
          <td>{{ limitText(c) }}</td>
          <td>{{ c.payment_terms_days }} days</td>
          <td>{{ c.is_active ? 'Active' : 'Inactive' }}</td>
          <td v-if="canManage"><button type="button" @click="startEdit(c)">Edit</button></td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
