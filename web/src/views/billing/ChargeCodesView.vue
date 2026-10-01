<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { ChargeCode, ChargeType, PriceMode, ServiceCharge, Tax } from '@/api/types'
import ChargeCalculator from '@/components/ChargeCalculator.vue'
import { useAccountNames } from './accountNames'
import GlAccountInput from './GlAccountInput.vue'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const { label: accountLabel } = useAccountNames()
const property = usePropertyStore()

const codes = ref<ChargeCode[]>([])
const taxes = ref<Tax[]>([])
const services = ref<ServiceCharge[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<ChargeCode | 'new' | null>(null)
const typeFilter = ref('')

const canManage = computed(() => auth.can('billing_config.manage', property.currentId))
const types: ChargeType[] = ['ROOM', 'FOOD_BEVERAGE', 'SERVICE', 'FEE', 'OTHER']
const typeLabel: Record<ChargeType, string> = { ROOM: 'Room', FOOD_BEVERAGE: 'Food & beverage', SERVICE: 'Service', FEE: 'Fee', OTHER: 'Other' }
const visible = computed(() => codes.value.filter((c) => !typeFilter.value || c.charge_type === typeFilter.value))

const blank = () => ({
  code: '', name: '', charge_type: 'OTHER' as ChargeType, price_mode: 'EXCLUSIVE' as PriceMode, default_unit_price: '', gl_account_code: '', is_active: true,
})
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

// The ordered rule editor works on copies: nothing changes until "Save rules".
interface RuleRow {
  id: number
  label: string
}
const taxRows = ref<RuleRow[]>([])
const serviceRows = ref<RuleRow[]>([])
const addTax = ref(0)
const addService = ref(0)
const rulesSaved = ref(false)
const rulesVersion = ref(0) // bumped when rules are saved, so the calculator recalculates

const freeTaxes = computed(() => taxes.value.filter((t) => t.is_active && !taxRows.value.some((r) => r.id === t.id)))
const freeServices = computed(() => services.value.filter((s) => s.is_active && !serviceRows.value.some((r) => r.id === s.id)))

function summary(c: ChargeCode): string {
  const parts = [...c.service_charges.map((s) => `${s.code} ${Number(s.rate)}%`), ...c.taxes.map((t) => `${t.code} ${Number(t.rate)}%`)]
  return parts.length ? parts.join(' → ') : 'none'
}

async function load(): Promise<void> {
  const propertyId = property.currentId
  codes.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    const path = { path: { propertyId } }
    const [c, t, s] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/charge-codes', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/taxes', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/service-charges', { params: { ...path, query: { limit: 200, cursor } } })),
    ])
    codes.value = c
    taxes.value = t
    services.value = s
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function loadRules(c: ChargeCode | null): void {
  taxRows.value = (c?.taxes ?? []).map((t) => ({ id: t.tax_id, label: `${t.code} · ${Number(t.rate)}%${t.tax_on_service ? ' (also on service)' : ''}` }))
  serviceRows.value = (c?.service_charges ?? []).map((s) => ({ id: s.service_charge_id, label: `${s.code} · ${Number(s.rate)}%` }))
  addTax.value = 0
  addService.value = 0
  rulesSaved.value = false
}

function startNew(): void {
  Object.assign(form, blank())
  loadRules(null)
  error.value = null
  editing.value = 'new'
}

function startEdit(c: ChargeCode): void {
  Object.assign(form, blank(), {
    code: c.code, name: c.name, charge_type: c.charge_type, price_mode: c.price_mode, default_unit_price: c.default_unit_price ?? '', gl_account_code: c.gl_account_code ?? '', is_active: c.is_active,
  })
  loadRules(c)
  error.value = null
  editing.value = c
}

function move(rows: RuleRow[], i: number, by: -1 | 1): void {
  const j = i + by
  const a = rows[i]
  const b = rows[j]
  if (!a || !b) return
  rows[i] = b
  rows[j] = a
}

function addRule(kind: 'tax' | 'service'): void {
  if (kind === 'tax') {
    const t = taxes.value.find((x) => x.id === Number(addTax.value))
    if (t) taxRows.value.push({ id: t.id, label: `${t.code} · ${Number(t.rate)}%${t.tax_on_service ? ' (also on service)' : ''}` })
    addTax.value = 0
  } else {
    const s = services.value.find((x) => x.id === Number(addService.value))
    if (s) serviceRows.value.push({ id: s.id, label: `${s.code} · ${Number(s.rate)}%` })
    addService.value = 0
  }
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/charge-codes', {
        params: { path: { propertyId } },
        body: {
          code: form.code, name: form.name, charge_type: form.charge_type, price_mode: form.price_mode,
          default_unit_price: form.default_unit_price || undefined, gl_account_code: form.gl_account_code || undefined, is_active: form.is_active,
        },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/charge-codes/{id}', {
        params: { path: { propertyId, id: editing.value.id } },
        body: {
          name: form.name, charge_type: form.charge_type, price_mode: form.price_mode,
          default_unit_price: form.default_unit_price, gl_account_code: form.gl_account_code, is_active: form.is_active,
        },
      })
    }
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

async function saveRules(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null || editing.value === 'new') return
  saving.value = true
  error.value = null
  rulesSaved.value = false
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/charge-codes/{id}/rules', {
      params: { path: { propertyId, id: editing.value.id } },
      body: {
        taxes: taxRows.value.map((r, i) => ({ tax_id: r.id, sequence: i + 1 })),
        service_charges: serviceRows.value.map((r, i) => ({ service_charge_id: r.id, sequence: i + 1 })),
      },
    })
    if (data) {
      editing.value = data
      loadRules(data)
      rulesSaved.value = true
      rulesVersion.value++
    }
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => property.currentId, load, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Charge codes</h1>
    <button v-if="canManage && !editing" type="button" class="btn-primary" @click="startNew">New charge code</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>

  <template v-else>
    <form v-if="editing" class="card" novalidate data-testid="code-form" @submit.prevent="save">
      <h2>{{ editing === 'new' ? 'New charge code' : `Edit ${form.code}` }}</h2>
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
          <span>Type</span>
          <select v-model="form.charge_type" name="charge_type" :disabled="editing !== 'new' && editing.is_system">
            <option v-for="t in types" :key="t" :value="t">{{ typeLabel[t] }}</option>
          </select>
          <small v-if="editing !== 'new' && editing.is_system" class="hint">The type of a system code is fixed.</small>
          <small v-if="fieldError('charge_type')" class="error-text">{{ fieldError('charge_type') }}</small>
        </label>
        <label class="field">
          <span>Prices are</span>
          <select v-model="form.price_mode" name="price_mode">
            <option value="EXCLUSIVE">Exclusive: service and tax are added</option>
            <option value="INCLUSIVE">Inclusive: service and tax are contained</option>
          </select>
          <small class="hint">Locked once the code is used by a rate plan or a posting.</small>
          <small v-if="fieldError('price_mode')" class="error-text">{{ fieldError('price_mode') }}</small>
        </label>
        <label class="field">
          <span>Default unit price</span>
          <input v-model="form.default_unit_price" name="default_unit_price" inputmode="decimal" :aria-invalid="!!fieldError('default_unit_price')" />
          <small v-if="fieldError('default_unit_price')" class="error-text">{{ fieldError('default_unit_price') }}</small>
        </label>
        <label class="field">
          <span>Revenue account</span>
          <GlAccountInput v-model="form.gl_account_code" kind="CHARGE_CODE" :invalid="!!fieldError('gl_account_code')" />
          <small class="hint">Account code in the chart of accounts. Optional; a posted item keeps the code it had when it was posted.</small>
          <small v-if="fieldError('gl_account_code')" class="error-text">{{ fieldError('gl_account_code') }}</small>
        </label>
        <label class="check">
          <input v-model="form.is_active" name="is_active" type="checkbox" />
          <span>Active</span>
        </label>
      </div>
      <div class="form-actions">
        <button type="button" @click="editing = null">Close</button>
        <button type="submit" class="btn-primary" :disabled="saving">Save</button>
      </div>

      <template v-if="editing !== 'new'">
        <h2 class="rules-title">Rules</h2>
        <p class="muted">Applied in this order. They affect future postings only.</p>
        <div class="rules" data-testid="rules">
          <div>
            <h3>Service charges</h3>
            <p v-if="!serviceRows.length" class="muted">None.</p>
            <ol>
              <li v-for="(r, i) in serviceRows" :key="r.id" :data-testid="`service-row-${i}`">
                <span>{{ r.label }}</span>
                <span class="row-actions">
                  <button type="button" :disabled="i === 0" :aria-label="`Move ${r.label} up`" @click="move(serviceRows, i, -1)">↑</button>
                  <button type="button" :disabled="i === serviceRows.length - 1" :aria-label="`Move ${r.label} down`" @click="move(serviceRows, i, 1)">↓</button>
                  <button type="button" :aria-label="`Remove ${r.label}`" @click="serviceRows.splice(i, 1)">Remove</button>
                </span>
              </li>
            </ol>
            <div v-if="freeServices.length" class="add">
              <select v-model="addService" name="add_service" aria-label="Add a service charge">
                <option :value="0">Add service charge…</option>
                <option v-for="s in freeServices" :key="s.id" :value="s.id">{{ s.code }} · {{ Number(s.rate) }}%</option>
              </select>
              <button type="button" :disabled="!addService" data-testid="add-service" @click="addRule('service')">Add</button>
            </div>
          </div>
          <div>
            <h3>Taxes</h3>
            <p v-if="!taxRows.length" class="muted">None.</p>
            <ol>
              <li v-for="(r, i) in taxRows" :key="r.id" :data-testid="`tax-row-${i}`">
                <span>{{ r.label }}</span>
                <span class="row-actions">
                  <button type="button" :disabled="i === 0" :aria-label="`Move ${r.label} up`" @click="move(taxRows, i, -1)">↑</button>
                  <button type="button" :disabled="i === taxRows.length - 1" :aria-label="`Move ${r.label} down`" @click="move(taxRows, i, 1)">↓</button>
                  <button type="button" :aria-label="`Remove ${r.label}`" @click="taxRows.splice(i, 1)">Remove</button>
                </span>
              </li>
            </ol>
            <div v-if="freeTaxes.length" class="add">
              <select v-model="addTax" name="add_tax" aria-label="Add a tax">
                <option :value="0">Add tax…</option>
                <option v-for="t in freeTaxes" :key="t.id" :value="t.id">{{ t.code }} · {{ Number(t.rate) }}%</option>
              </select>
              <button type="button" :disabled="!addTax" data-testid="add-tax" @click="addRule('tax')">Add</button>
            </div>
          </div>
        </div>
        <div class="form-actions">
          <span v-if="rulesSaved" class="muted" role="status" data-testid="rules-saved">Rules saved.</span>
          <button type="button" class="btn-primary" :disabled="saving || !canManage" data-testid="save-rules" @click="saveRules">Save rules</button>
        </div>
        <ChargeCalculator
          :key="editing.id"
          :charge-code-id="editing.id"
          :price-mode="editing.price_mode"
          :default-unit-price="editing.default_unit_price"
          :version="rulesVersion"
        />
      </template>
    </form>

    <section class="card">
      <label v-if="codes.length" class="field filter">
        <span>Type</span>
        <select v-model="typeFilter" name="type_filter">
          <option value="">All types</option>
          <option v-for="t in types" :key="t" :value="t">{{ typeLabel[t] }}</option>
        </select>
      </label>
      <p v-if="loaded && !codes.length" class="muted" data-testid="empty">No charge codes.</p>
      <table v-else-if="codes.length" class="list">
        <thead>
          <tr>
            <th>Code</th>
            <th>Name</th>
            <th>Type</th>
            <th>Prices</th>
            <th>Rules (in order)</th>
            <th>Account</th>
            <th>Status</th>
            <th v-if="canManage" />
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in visible" :key="c.id" :data-testid="`code-${c.code}`">
            <td>
              <b>{{ c.code }}</b>
              <small v-if="c.is_system" class="muted"> system</small>
            </td>
            <td>{{ c.name }}</td>
            <td>{{ typeLabel[c.charge_type] }}</td>
            <td>{{ c.price_mode === 'INCLUSIVE' ? 'Inclusive' : 'Exclusive' }}</td>
            <td data-testid="summary">{{ summary(c) }}</td>
            <td data-testid="account">{{ accountLabel(c.gl_account_code) }}</td>
            <td>{{ c.is_active ? 'Active' : 'Inactive' }}</td>
            <td v-if="canManage"><button type="button" @click="startEdit(c)">Edit</button></td>
          </tr>
        </tbody>
      </table>
    </section>
  </template>
</template>

<style scoped>
.filter {
  max-width: 220px;
  margin-bottom: 12px;
}
.rules-title {
  margin-top: 24px;
}
.rules {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 16px 24px;
}
.rules h3 {
  margin: 0 0 6px;
  font-size: 14px;
}
.rules ol {
  margin: 0 0 8px;
  padding-left: 20px;
}
.rules li {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  padding: 4px 0;
}
.row-actions {
  display: flex;
  gap: 4px;
}
.add {
  display: flex;
  gap: 8px;
}
.add select {
  font: inherit;
  padding: 6px 8px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
}
</style>
