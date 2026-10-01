<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GlAccount } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { listAccounts } from './accountApi'
import { ACCOUNT_TYPES, GROUPS, TYPE_LABEL, toTree } from './accountMeta'

const auth = useAuthStore()
const property = usePropertyStore()

const accounts = ref<GlAccount[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const filter = reactive({ q: '', type: '', inactive: false, headers: true })
const editing = ref<GlAccount | 'new' | null>(null)
const form = reactive({ code: '', name: '', account_type: 'REVENUE', normal_side: '', parent_id: 0, is_postable: true, is_active: true, statement_group: '', description: '' })
const importing = ref(false)
const importText = ref('')
const importResult = ref<{ dry_run: boolean; created: number; updated: number } | null>(null)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const groups = computed(() => GROUPS[form.account_type] ?? [])
const parents = computed(() => accounts.value.filter((a) => !a.is_postable && a.account_type === form.account_type && (editing.value === 'new' || a.id !== (editing.value as GlAccount | null)?.id)))

const visible = computed(() => {
  const q = filter.q.trim().toLowerCase()
  const list = accounts.value.filter(
    (a) => (!filter.type || a.account_type === filter.type) && (filter.inactive || a.is_active) && (filter.headers || a.is_postable)
      && (!q || a.code.toLowerCase().includes(q) || a.name.toLowerCase().includes(q)),
  )
  return toTree(list)
})

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  try {
    accounts.value = await listAccounts(propertyId)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(parent?: GlAccount): void {
  Object.assign(form, {
    code: '', name: '', account_type: parent?.account_type ?? 'REVENUE', normal_side: '', parent_id: parent?.id ?? 0, is_postable: true, is_active: true,
    statement_group: parent?.statement_group ?? '', description: '',
  })
  error.value = null
  editing.value = 'new'
}

function startEdit(a: GlAccount): void {
  Object.assign(form, {
    code: a.code, name: a.name, account_type: a.account_type, normal_side: a.normal_side, parent_id: a.parent_id ?? 0, is_postable: a.is_postable, is_active: a.is_active,
    statement_group: a.statement_group ?? '', description: a.description ?? '',
  })
  error.value = null
  editing.value = a
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || editing.value === null) return
  busy.value = true
  error.value = null
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/accounting/accounts', {
        params: { path: { propertyId } },
        body: {
          code: form.code, name: form.name, account_type: form.account_type as 'REVENUE', normal_side: (form.normal_side || undefined) as 'DEBIT' | undefined,
          parent_id: form.parent_id || null, is_postable: form.is_postable, is_active: form.is_active, statement_group: form.statement_group || undefined,
          description: form.description || undefined,
        },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/accounting/accounts/{id}', {
        params: { path: { propertyId, id: editing.value.id } },
        body: {
          name: form.name, parent_id: form.parent_id, is_postable: form.is_postable, is_active: form.is_active, statement_group: form.statement_group,
          description: form.description,
        },
      })
    }
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function remove(a: GlAccount): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !window.confirm(`Delete account ${a.code} ${a.name}?`)) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.DELETE('/api/v1/properties/{propertyId}/accounting/accounts/{id}', { params: { path: { propertyId, id: a.id } } })
    notice.value = `Account ${a.code} deleted.`
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function exportCsv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const get = api.GET as unknown as (path: string, init: object) => Promise<{ data?: unknown }>
    const { data } = await get('/api/v1/properties/{propertyId}/accounting/accounts', { params: { path: { propertyId }, query: { format: 'csv' } }, parseAs: 'text' })
    const url = URL.createObjectURL(new Blob([String(data ?? '')], { type: 'text/csv;charset=utf-8' }))
    const a = document.createElement('a')
    a.href = url
    a.download = 'chart-of-accounts.csv'
    a.click()
    URL.revokeObjectURL(url)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function readFile(ev: Event): Promise<void> {
  const file = (ev.target as HTMLInputElement).files?.[0]
  if (file) importText.value = await file.text()
}

async function runImport(dryRun: boolean): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  importResult.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/accounting/accounts/import', {
      params: { path: { propertyId } }, body: { csv: importText.value, dry_run: dryRun },
    })
    importResult.value = data ?? null
    if (!dryRun) {
      notice.value = `Imported: ${data?.created ?? 0} added, ${data?.updated ?? 0} updated.`
      importing.value = false
      importText.value = ''
      await load()
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  accounts.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Chart of accounts</h1>
    <div class="head-actions">
      <button v-if="can('accounting.view')" type="button" data-testid="export" @click="exportCsv">Export CSV</button>
      <button v-if="can('accounting.manage') && !importing" type="button" data-testid="open-import" @click="importing = true">Import CSV</button>
      <button v-if="can('accounting.manage') && !editing" type="button" class="btn-primary" data-testid="new-account" @click="startNew()">New account</button>
    </div>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="coa-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-if="error.fieldErrors?.length">
      <br />
      <span v-for="(f, i) in error.fieldErrors.slice(0, 8)" :key="i" class="muted">{{ f.field }}: {{ f.message }}<br /></span>
    </template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">Your role at this property cannot see the chart of accounts: the <code>accounting.view</code> permission is needed.</p>

  <template v-else>
    <form v-if="editing" class="card" novalidate data-testid="account-form" @submit.prevent="save">
      <h2>{{ editing === 'new' ? 'New account' : `Edit ${form.code}` }}</h2>
      <div class="form-grid">
        <label class="field">
          <span>Code</span>
          <input v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="!!fieldError('code')" />
          <small v-if="fieldError('code')" class="error-text">{{ fieldError('code') }}</small>
        </label>
        <label class="field">
          <span>Name</span>
          <input v-model="form.name" name="name" maxlength="150" :aria-invalid="!!fieldError('name')" />
          <small v-if="fieldError('name')" class="error-text">{{ fieldError('name') }}</small>
        </label>
        <label class="field">
          <span>Type</span>
          <select v-model="form.account_type" name="account_type" :disabled="editing !== 'new'" @change="form.statement_group = ''; form.parent_id = 0">
            <option v-for="t in ACCOUNT_TYPES" :key="t" :value="t">{{ TYPE_LABEL[t] }}</option>
          </select>
        </label>
        <label v-if="editing === 'new'" class="field">
          <span>Normal balance</span>
          <select v-model="form.normal_side" name="normal_side">
            <option value="">Usual for the type</option>
            <option value="DEBIT">Debit</option>
            <option value="CREDIT">Credit (a contra account)</option>
          </select>
        </label>
        <label class="field">
          <span>Under</span>
          <select v-model.number="form.parent_id" name="parent_id" :aria-invalid="!!fieldError('parent_id')">
            <option :value="0">Top level</option>
            <option v-for="p in parents" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option>
          </select>
          <small v-if="fieldError('parent_id')" class="error-text">{{ fieldError('parent_id') }}</small>
        </label>
        <label class="field">
          <span>Statement group</span>
          <select v-model="form.statement_group" name="statement_group" :aria-invalid="!!fieldError('statement_group')">
            <option value="">None (a header only)</option>
            <option v-for="g in groups" :key="g.value" :value="g.value">{{ g.label }}</option>
          </select>
          <small v-if="fieldError('statement_group')" class="error-text">{{ fieldError('statement_group') }}</small>
        </label>
        <label class="field wide">
          <span>Description</span>
          <input v-model="form.description" name="description" maxlength="300" />
        </label>
        <label class="check">
          <input v-model="form.is_postable" name="is_postable" type="checkbox" />
          <span>Takes postings (not a header)</span>
        </label>
        <label class="check">
          <input v-model="form.is_active" name="is_active" type="checkbox" />
          <span>Active</span>
        </label>
      </div>
      <div class="form-actions">
        <button type="button" @click="editing = null">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy">Save</button>
      </div>
    </form>

    <form v-if="importing" class="card" novalidate data-testid="import-form" @submit.prevent="runImport(false)">
      <h2>Import accounts from CSV</h2>
      <p class="muted">A header row with code, name and type (and optionally parent_code, postable, group, active, description). A row whose code exists updates that account. Nothing is changed unless the whole file is valid.</p>
      <input type="file" accept=".csv,text/csv" data-testid="import-file" @change="readFile" />
      <textarea v-model="importText" name="csv" rows="8" class="csv" placeholder="code,name,type,parent_code,postable,group,active" />
      <p v-if="importResult" class="notice" data-testid="import-result">
        {{ importResult.dry_run ? 'The file is valid: it would add' : 'Added' }} {{ importResult.created }} and update {{ importResult.updated }} account(s).
      </p>
      <div class="form-actions">
        <button type="button" @click="importing = false; importResult = null">Cancel</button>
        <button type="button" :disabled="busy || !importText.trim()" data-testid="import-check" @click="runImport(true)">Check the file</button>
        <button type="submit" class="btn-primary" :disabled="busy || !importText.trim()" data-testid="import-run">Import</button>
      </div>
    </form>

    <section class="card">
      <form class="filters" novalidate @submit.prevent>
        <label class="field">
          <span>Search</span>
          <input v-model="filter.q" name="q" type="search" placeholder="Code or name" />
        </label>
        <label class="field">
          <span>Type</span>
          <select v-model="filter.type" name="filter_type">
            <option value="">All</option>
            <option v-for="t in ACCOUNT_TYPES" :key="t" :value="t">{{ TYPE_LABEL[t] }}</option>
          </select>
        </label>
        <label class="check"><input v-model="filter.headers" name="headers" type="checkbox" /><span>Show header accounts</span></label>
        <label class="check"><input v-model="filter.inactive" name="inactive" type="checkbox" /><span>Show inactive</span></label>
      </form>
      <p v-if="loaded && !visible.length" class="muted" data-testid="empty">No accounts match.</p>
      <table v-else class="list" data-testid="accounts">
        <thead><tr><th>Code</th><th>Name</th><th>Type</th><th>Normal</th><th>Group</th><th>Status</th><th v-if="can('accounting.manage')" /></tr></thead>
        <tbody>
          <tr v-for="r in visible" :key="r.account.id" :class="{ header: !r.account.is_postable, off: !r.account.is_active }" :data-testid="`account-${r.account.code}`">
            <td><span :style="{ paddingLeft: `${r.depth * 18}px` }"><b v-if="!r.account.is_postable">{{ r.account.code }}</b><template v-else>{{ r.account.code }}</template></span></td>
            <td>{{ r.account.name }}</td>
            <td>{{ r.account.account_type.toLowerCase() }}</td>
            <td>{{ r.account.normal_side.toLowerCase() }}</td>
            <td><small class="muted">{{ r.account.statement_group }}</small></td>
            <td>{{ r.account.is_active ? '' : 'Inactive' }}</td>
            <td v-if="can('accounting.manage')" class="row-actions">
              <button v-if="!r.account.is_postable" type="button" :data-testid="`add-under-${r.account.code}`" @click="startNew(r.account)">Add under</button>
              <button type="button" :data-testid="`edit-${r.account.code}`" @click="startEdit(r.account)">Edit</button>
              <button v-if="!r.account.in_use" type="button" :data-testid="`delete-${r.account.code}`" @click="remove(r.account)">Delete</button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </template>
</template>

<style scoped>
.head-actions {
  display: flex;
  gap: 8px;
}
.header td {
  background: var(--accent-soft);
}
.off td {
  color: var(--muted, #6b7280);
}
.wide {
  grid-column: 1 / -1;
}
.csv {
  width: 100%;
  font-family: monospace;
  margin: 8px 0;
}
</style>
