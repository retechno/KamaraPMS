<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { TaxFilingProfile } from '@/api/types'
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
}

const profiles = ref<TaxFilingProfile[]>([])
const taxes = ref<TaxRow[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const editing = ref<TaxFilingProfile | 'new' | null>(null)
const form = reactive({ tax_id: 0, authority: '', registration_number: '', due_day: '15', is_active: true })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const choices = computed(() => {
  const taken = new Set(profiles.value.map((p) => p.tax_id))
  return taxes.value.filter((t) => !taken.has(t.id))
})

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('tax.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/profiles', { params: { path: { propertyId } } })
    profiles.value = data?.data ?? []
    if (can('tax.manage') && !taxes.value.length) {
      const res = await api.GET('/api/v1/properties/{propertyId}/taxes', { params: { path: { propertyId } } })
      taxes.value = ((res.data as { data?: TaxRow[] } | undefined)?.data ?? []).filter((t) => t.is_active)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, { tax_id: 0, authority: '', registration_number: '', due_day: '15', is_active: true })
  error.value = null
  editing.value = 'new'
}

function startEdit(p: TaxFilingProfile): void {
  Object.assign(form, { tax_id: p.tax_id, authority: p.authority, registration_number: p.registration_number ?? '', due_day: String(p.due_day), is_active: p.is_active })
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
        body: { tax_id: form.tax_id, authority: form.authority, registration_number: form.registration_number || undefined, due_day: Number(form.due_day), is_active: form.is_active },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/tax/profiles/{id}', {
        params: { path: { propertyId, id: editing.value.id } },
        body: { authority: form.authority, registration_number: form.registration_number, due_day: Number(form.due_day), is_active: form.is_active },
      })
    }
    notice.value = 'Saved.'
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
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
  <div class="page-head">
    <h1 class="page-title">How taxes are filed</h1>
    <button v-if="can('tax.manage') && !editing" type="button" class="btn-primary" data-testid="new-profile" @click="startNew">Set up a tax</button>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="profile-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 4)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('tax.view')" class="muted" data-testid="no-access">Your role at this property cannot see tax filing: the <code>tax.view</code> permission is needed.</p>
  <template v-else>
    <p class="muted">
      For each tax the hotel collects from guests (the hotel tax, VAT), say which authority it is filed with, the hotel's registration number there and the day of the next month the return and the payment are due.
      The taxes themselves are set up under Setup → Taxes.
    </p>
    <form v-if="editing" class="card" novalidate data-testid="profile-form" @submit.prevent="save">
      <h2>{{ editing === 'new' ? 'Set up a tax' : `Edit ${editing.tax_code}` }}</h2>
      <div class="form-grid">
        <label v-if="editing === 'new'" class="field">
          <span>Tax</span>
          <select v-model.number="form.tax_id" name="tax_id" :aria-invalid="!!fieldError('tax_id')">
            <option :value="0">Choose a tax</option>
            <option v-for="t in choices" :key="t.id" :value="t.id">{{ t.code }} · {{ t.name }} ({{ Number(t.rate) }}%)</option>
          </select>
          <small v-if="fieldError('tax_id')" class="error-text">{{ fieldError('tax_id') }}</small>
        </label>
        <label class="field">
          <span>Tax authority</span>
          <input v-model="form.authority" name="authority" maxlength="150" placeholder="e.g. Bapenda Kabupaten Badung" :aria-invalid="!!fieldError('authority')" />
          <small v-if="fieldError('authority')" class="error-text">{{ fieldError('authority') }}</small>
        </label>
        <label class="field"><span>Registration number (NPWPD)</span><input v-model="form.registration_number" name="registration_number" maxlength="60" /></label>
        <label class="field">
          <span>Due day of the next month</span>
          <input v-model="form.due_day" name="due_day" inputmode="numeric" :aria-invalid="!!fieldError('due_day')" />
          <small v-if="fieldError('due_day')" class="error-text">{{ fieldError('due_day') }}</small>
        </label>
        <label class="check"><input v-model="form.is_active" name="is_active" type="checkbox" /><span>Filed (takes returns)</span></label>
      </div>
      <div class="form-actions">
        <button type="button" @click="editing = null">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy || (editing === 'new' && !form.tax_id) || !form.authority.trim()">Save</button>
      </div>
    </form>
    <section class="card">
      <p v-if="loaded && !profiles.length" class="muted" data-testid="empty">No tax is set up for filing yet.</p>
      <table v-else class="list" data-testid="profiles">
        <thead><tr><th>Tax</th><th>Authority</th><th>Registration number</th><th>Due</th><th>Status</th><th /></tr></thead>
        <tbody>
          <tr v-for="p in profiles" :key="p.id" :class="{ off: !p.is_active }" :data-testid="`profile-${p.tax_code}`">
            <td><b>{{ p.tax_code }}</b> · {{ p.tax_name }} <small class="muted">{{ Number(p.tax_rate) }}%</small></td>
            <td>{{ p.authority }}</td>
            <td>{{ p.registration_number ?? '—' }}</td>
            <td>day {{ p.due_day }}</td>
            <td>{{ p.is_active ? 'Filed' : 'Not filed' }}</td>
            <td class="row-actions">
              <RouterLink :to="{ path: '/tax/returns', query: { tax: String(p.tax_id) } }">Returns</RouterLink>
              <button v-if="can('tax.manage')" type="button" :data-testid="`edit-${p.tax_code}`" @click="startEdit(p)">Edit</button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </template>
</template>

<style scoped>
.off td {
  color: var(--muted, #6b7280);
}
.row-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}
</style>
