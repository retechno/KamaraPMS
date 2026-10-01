<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GlAccountInput from './GlAccountInput.vue'

/** Taxes and service charges differ only in `tax_on_service`; one section edits either list. */
const props = defineProps<{ kind: 'tax' | 'service' }>()

interface Item {
  id: number
  code: string
  name: string
  rate: string
  is_active: boolean
  tax_on_service?: boolean
  gl_account_code: string | null
}

const auth = useAuthStore()
const property = usePropertyStore()

const items = ref<Item[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const saving = ref(false)
const editing = ref<Item | 'new' | null>(null)

const isTax = computed(() => props.kind === 'tax')
const title = computed(() => (isTax.value ? 'Taxes' : 'Service charges'))
const canManage = computed(() => auth.can('billing_config.manage', property.currentId))
const blank = () => ({ code: '', name: '', rate: '', tax_on_service: false, gl_account_code: '', is_active: true })
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function load(): Promise<void> {
  const propertyId = property.currentId
  items.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    items.value = await (isTax.value
      ? fetchAll<Item>((cursor) => api.GET('/api/v1/properties/{propertyId}/taxes', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
      : fetchAll<Item>((cursor) => api.GET('/api/v1/properties/{propertyId}/service-charges', { params: { path: { propertyId }, query: { limit: 200, cursor } } })))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, blank())
  error.value = null
  notice.value = ''
  editing.value = 'new'
}

function startEdit(i: Item): void {
  Object.assign(form, blank(), { code: i.code, name: i.name, rate: i.rate, tax_on_service: i.tax_on_service ?? false, gl_account_code: i.gl_account_code ?? '', is_active: i.is_active })
  error.value = null
  notice.value = ''
  editing.value = i
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  try {
    let saved: Item | undefined
    if (editing.value === 'new') {
      saved = isTax.value
        ? (await api.POST('/api/v1/properties/{propertyId}/taxes', {
            params: { path: { propertyId } },
            body: { code: form.code, name: form.name, rate: form.rate, tax_on_service: form.tax_on_service, gl_account_code: form.gl_account_code || undefined, is_active: form.is_active },
          })).data
        : (await api.POST('/api/v1/properties/{propertyId}/service-charges', {
            params: { path: { propertyId } },
            body: { code: form.code, name: form.name, rate: form.rate, gl_account_code: form.gl_account_code || undefined, is_active: form.is_active },
          })).data
    } else {
      const id = editing.value.id
      saved = isTax.value
        ? (await api.PATCH('/api/v1/properties/{propertyId}/taxes/{id}', {
            params: { path: { propertyId, id } },
            body: { name: form.name, rate: form.rate, tax_on_service: form.tax_on_service, gl_account_code: form.gl_account_code, is_active: form.is_active },
          })).data
        : (await api.PATCH('/api/v1/properties/{propertyId}/service-charges/{id}', {
            params: { path: { propertyId, id } },
            body: { name: form.name, rate: form.rate, gl_account_code: form.gl_account_code, is_active: form.is_active },
          })).data
    }
    const affected = (saved as { affected_open_stays?: number } | undefined)?.affected_open_stays
    notice.value = affected ? `Saved. ${affected} in-house stay(s) will be charged at the new rate from now on.` : ''
    editing.value = null
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
  <section class="card" :data-testid="`section-${kind}`">
    <div class="page-head">
      <h2>{{ title }}</h2>
      <button v-if="canManage && !editing" type="button" @click="startNew">New {{ isTax ? 'tax' : 'service charge' }}</button>
    </div>

    <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
    <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>

    <form v-if="editing" novalidate class="edit" @submit.prevent="save">
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
          <span>Rate (%)</span>
          <input v-model="form.rate" name="rate" inputmode="decimal" placeholder="11" :aria-invalid="!!fieldError('rate')" />
          <small class="hint">0 to 100, up to 4 decimals.</small>
          <small v-if="fieldError('rate')" class="error-text">{{ fieldError('rate') }}</small>
        </label>
        <label class="field">
          <span>{{ isTax ? 'Tax payable account' : 'Service payable account' }}</span>
          <GlAccountInput v-model="form.gl_account_code" :kind="isTax ? 'TAX' : 'SERVICE_CHARGE'" :invalid="!!fieldError('gl_account_code')" />
          <small class="hint">Account code in the chart of accounts. Optional; a posted item keeps the code it had when it was posted.</small>
          <small v-if="fieldError('gl_account_code')" class="error-text">{{ fieldError('gl_account_code') }}</small>
        </label>
        <label v-if="isTax" class="check">
          <input v-model="form.tax_on_service" name="tax_on_service" type="checkbox" />
          <span>Also levied on service charges</span>
        </label>
        <label class="check">
          <input v-model="form.is_active" name="is_active" type="checkbox" />
          <span>Active</span>
        </label>
      </div>
      <div class="form-actions">
        <button type="button" @click="editing = null">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="saving">Save</button>
      </div>
    </form>

    <p v-if="loaded && !items.length" class="muted" data-testid="empty">None yet.</p>
    <table v-else-if="items.length" class="list">
      <thead>
        <tr>
          <th>Code</th>
          <th>Name</th>
          <th>Rate</th>
          <th>Account</th>
          <th v-if="isTax">On service</th>
          <th>Status</th>
          <th v-if="canManage" />
        </tr>
      </thead>
      <tbody>
        <tr v-for="i in items" :key="i.id" :data-testid="`${kind}-${i.code}`">
          <td><b>{{ i.code }}</b></td>
          <td>{{ i.name }}</td>
          <td>{{ Number(i.rate) }}%</td>
          <td data-testid="account">{{ i.gl_account_code ?? '—' }}</td>
          <td v-if="isTax">{{ i.tax_on_service ? 'Yes' : 'No' }}</td>
          <td>{{ i.is_active ? 'Active' : 'Inactive' }}</td>
          <td v-if="canManage"><button type="button" @click="startEdit(i)">Edit</button></td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<style scoped>
.edit {
  margin-bottom: 16px;
}
</style>
