<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GlAccount } from '@/api/types'
import { confirm } from '@/composables/useConfirm'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { listAccounts } from './accountApi'
import { ACCOUNT_TYPES, GROUPS, toTree, type TreeRow } from './accountMeta'

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

const typeLabel = (k: string): string => t(`accountingBooks.type_${k}` as 'accountingBooks.type_ASSET')
const groupLabel = (k: string): string => t(`accountingBooks.g_${k}` as 'accountingBooks.g_CASH')
const columns = computed<Column<TreeRow>[]>(() => [
  { key: 'code', label: t('accountingBooks.code') },
  { key: 'name', label: t('accountingBooks.name') },
  { key: 'type', label: t('accountingBooks.type') },
  { key: 'normal', label: t('accountingBooks.normal') },
  { key: 'group', label: t('accountingBooks.group') },
  { key: 'status', label: t('setup.status') },
  ...(can('accounting.manage') ? [{ key: 'actions', label: '', align: 'right' as const }] : []),
])

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
  if (propertyId === null || !(await confirm({ title: t('accountingBooks.deleteTitle', { code: a.code, name: a.name }), destructive: true }))) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.DELETE('/api/v1/properties/{propertyId}/accounting/accounts/{id}', { params: { path: { propertyId, id: a.id } } })
    notice.value = t('accountingBooks.deleted', { code: a.code })
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
      notice.value = t('accountingBooks.imported', { created: data?.created ?? 0, updated: data?.updated ?? 0 })
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
  <PageHeader :title="t('accountingBooks.coaTitle')">
    <template #actions>
      <Button v-if="can('accounting.view')" type="button" variant="outline" data-testid="export" @click="exportCsv">{{ t('accountingBooks.coaExport') }}</Button>
      <Button v-if="can('accounting.manage') && !importing" type="button" variant="outline" data-testid="open-import" @click="importing = true">{{ t('accountingBooks.coaImport') }}</Button>
      <Button v-if="can('accounting.manage') && !editing" type="button" data-testid="new-account" @click="startNew()">{{ t('accountingBooks.coaNew') }}</Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="coa-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-if="error.fieldErrors?.length">
      <br />
      <span v-for="(f, i) in error.fieldErrors.slice(0, 8)" :key="i" class="muted">{{ f.field }}: {{ f.message }}<br /></span>
    </template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('accountingBooks.coaNoAccess', { permission: 'accounting.view' }) }}</p>

  <template v-else>
    <Card v-if="editing" class="mb-4">
      <form novalidate data-testid="account-form" @submit.prevent="save">
        <CardHeader><CardTitle>{{ editing === 'new' ? t('accountingBooks.coaNew') : t('accountingBooks.coaEdit', { code: form.code }) }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <FormField :label="t('accountingBooks.code')" :error="fieldError('code')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('accountingBooks.name')" :error="fieldError('name')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" maxlength="150" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('accountingBooks.type')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.account_type" name="account_type" :disabled="editing !== 'new'" @change="form.statement_group = ''; form.parent_id = 0">
                  <option v-for="ty in ACCOUNT_TYPES" :key="ty" :value="ty">{{ typeLabel(ty) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField v-if="editing === 'new'" :label="t('accountingBooks.normalBalance')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.normal_side" name="normal_side">
                  <option value="">{{ t('accountingBooks.usualForType') }}</option>
                  <option value="DEBIT">{{ t('accountingBooks.debit') }}</option>
                  <option value="CREDIT">{{ t('accountingBooks.creditContra') }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('accountingBooks.under')" :error="fieldError('parent_id')">
              <template #default="{ id, invalid }">
                <Combobox :id="id" v-model="form.parent_id" name="parent_id" :aria-invalid="invalid" :options="[{ value: 0, label: `${t('accountingBooks.topLevel')}` }, ...parents.map((p) => ({ value: p.id, label: `${p.code} · ${p.name}` }))]" />
              </template>
            </FormField>
            <FormField :label="t('accountingBooks.statementGroup')" :error="fieldError('statement_group')">
              <template #default="{ id, invalid }">
                <NativeSelect :id="id" v-model="form.statement_group" name="statement_group" :aria-invalid="invalid">
                  <option value="">{{ t('accountingBooks.noneHeader') }}</option>
                  <option v-for="g in groups" :key="g.value" :value="g.value">{{ groupLabel(g.value) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField class="sm:col-span-2 lg:col-span-3" :label="t('accountingBooks.description')">
              <template #default="{ id }"><Input :id="id" v-model="form.description" name="description" maxlength="300" /></template>
            </FormField>
            <label class="flex items-center gap-2 text-sm">
              <input v-model="form.is_postable" name="is_postable" type="checkbox" class="size-4 accent-primary" /><span>{{ t('accountingBooks.takesPostings') }}</span>
            </label>
            <label class="flex items-center gap-2 text-sm">
              <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" /><span>{{ t('accountingBooks.active') }}</span>
            </label>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy">{{ t('common.save') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card v-if="importing" class="mb-4">
      <form novalidate data-testid="import-form" @submit.prevent="runImport(false)">
        <CardHeader>
          <CardTitle>{{ t('accountingBooks.importTitle') }}</CardTitle>
          <p class="m-0 text-sm text-muted-foreground">{{ t('accountingBooks.importHint') }}</p>
        </CardHeader>
        <CardContent>
          <input type="file" accept=".csv,text/csv" class="block text-sm" data-testid="import-file" @change="readFile" />
          <textarea
            v-model="importText"
            name="csv"
            rows="8"
            class="mt-2 w-full rounded-md border border-border bg-card p-3 font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            placeholder="code,name,type,parent_code,postable,group,active"
          />
          <p v-if="importResult" class="notice" data-testid="import-result">
            {{ importResult.dry_run ? t('accountingBooks.importValid', { created: importResult.created, updated: importResult.updated }) : t('accountingBooks.importDone', { created: importResult.created, updated: importResult.updated }) }}
          </p>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="importing = false; importResult = null">{{ t('common.cancel') }}</Button>
            <Button type="button" variant="outline" :disabled="busy || !importText.trim()" data-testid="import-check" @click="runImport(true)">{{ t('accountingBooks.checkFile') }}</Button>
            <Button type="submit" :disabled="busy || !importText.trim()" data-testid="import-run">{{ t('accountingBooks.import') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card>
      <CardContent class="pt-4">
        <form class="mb-4 flex flex-wrap items-end gap-4" novalidate @submit.prevent>
          <FormField class="w-64" :label="t('accountingBooks.search')">
            <template #default="{ id }"><Input :id="id" v-model="filter.q" name="q" type="search" :placeholder="t('accountingBooks.codeOrName')" /></template>
          </FormField>
          <FormField class="w-48" :label="t('accountingBooks.type')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.type" name="filter_type">
                <option value="">{{ t('accountingBooks.all') }}</option>
                <option v-for="ty in ACCOUNT_TYPES" :key="ty" :value="ty">{{ typeLabel(ty) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <label class="flex items-center gap-2 pb-2 text-sm"><input v-model="filter.headers" name="headers" type="checkbox" class="size-4 accent-primary" /><span>{{ t('accountingBooks.showHeaders') }}</span></label>
          <label class="flex items-center gap-2 pb-2 text-sm"><input v-model="filter.inactive" name="inactive" type="checkbox" class="size-4 accent-primary" /><span>{{ t('accountingBooks.showInactive') }}</span></label>
        </form>
        <EmptyState v-if="loaded && !visible.length" :title="t('accountingBooks.coaEmpty')" data-testid="empty" />
        <DataTable
          v-else
          :columns="columns"
          :rows="visible"
          :row-key="(r) => r.account.id"
          :row-test-id="(r) => `account-${r.account.code}`"
          :row-class="(r) => [!r.account.is_postable && 'header bg-accent/60', !r.account.is_active && 'text-muted-foreground'].filter(Boolean).join(' ') || undefined"
          :caption="t('accountingBooks.coaTitle')"
          data-testid="accounts"
        >
          <template #cell-code="{ row }"><span :style="{ paddingLeft: `${row.depth * 18}px` }"><b v-if="!row.account.is_postable">{{ row.account.code }}</b><template v-else>{{ row.account.code }}</template></span></template>
          <template #cell-name="{ row }">{{ row.account.name }}</template>
          <template #cell-type="{ row }">{{ typeLabel(row.account.account_type) }}</template>
          <template #cell-normal="{ row }">{{ t(`accountingBooks.side_${row.account.normal_side}` as 'accountingBooks.side_DEBIT') }}</template>
          <template #cell-group="{ row }"><small class="text-muted-foreground">{{ row.account.statement_group ? groupLabel(row.account.statement_group) : '' }}</small></template>
          <template #cell-status="{ row }"><Badge v-if="!row.account.is_active" variant="outline">{{ t('accountingBooks.inactive') }}</Badge></template>
          <template #cell-actions="{ row }">
            <div class="flex justify-end gap-1.5">
              <Button v-if="!row.account.is_postable" type="button" variant="outline" size="sm" :data-testid="`add-under-${row.account.code}`" @click="startNew(row.account)">{{ t('accountingBooks.addUnder') }}</Button>
              <Button type="button" variant="outline" size="sm" :data-testid="`edit-${row.account.code}`" @click="startEdit(row.account)">{{ t('common.edit') }}</Button>
              <Button v-if="!row.account.in_use" type="button" variant="outline" size="sm" :data-testid="`delete-${row.account.code}`" @click="remove(row.account)">{{ t('common.delete') }}</Button>
            </div>
          </template>
        </DataTable>
      </CardContent>
    </Card>
  </template>
</template>
