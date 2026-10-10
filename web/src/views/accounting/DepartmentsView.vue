<script setup lang="ts">
import RowMenu, { type RowMenuItem } from '@/components/app/RowMenu.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Department, DepartmentSetupReport } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { confirm } from '@/composables/useConfirm'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/**
 * The departments of the property and their sub-departments (two levels). The code and the parent never change, so what was posted keeps its place:
 * a department that is in use is switched off, not deleted.
 */
const auth = useAuthStore()
const property = usePropertyStore()

const departments = ref<Department[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const adding = ref(false)
const editing = ref<{ id: number; name: string; sort_order: string } | null>(null)
const form = reactive({ code: '', name: '', parent_id: '', sort_order: '' })
const setup = ref<DepartmentSetupReport | null>(null)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const tops = computed(() => departments.value.filter((d) => d.level === 1 && d.is_active))

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/departments', { params: { path: { propertyId } } })
    departments.value = data?.data ?? []
    const check = await api.GET('/api/v1/properties/{propertyId}/accounting/department-setup', { params: { path: { propertyId } } })
    setup.value = Array.isArray(check.data?.issues) ? check.data : null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function run(action: () => Promise<void>): Promise<boolean> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await action()
    await load()
    return true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    return false
  } finally {
    busy.value = false
  }
}

function startAdding(parent?: Department): void {
  Object.assign(form, { code: '', name: '', parent_id: parent ? String(parent.id) : '', sort_order: '' })
  error.value = null
  adding.value = true
}

async function create(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const ok = await run(async () => {
    await api.POST('/api/v1/properties/{propertyId}/departments', {
      params: { path: { propertyId } },
      body: { code: form.code.trim(), name: form.name.trim(), parent_id: form.parent_id ? Number(form.parent_id) : null, ...(form.sort_order.trim() ? { sort_order: Number(form.sort_order) } : {}) },
    })
    notice.value = t('departments.created', { code: form.code.trim().toUpperCase() })
  })
  if (ok) adding.value = false
}

// The same actions as the buttons, for a phone.
const rowItems = (d: Department): RowMenuItem[] => [
  ...(d.level === 1 && d.is_active ? [{ key: 'sub', label: t('departments.addSub') }] : []),
  { key: 'edit', label: t('common.edit') },
  { key: 'toggle', label: d.is_active ? t('departments.switchOff') : t('departments.switchOn'), disabled: busy.value },
  ...(!d.in_use && d.child_count === 0 ? [{ key: 'delete', label: t('common.delete'), destructive: true }] : []),
]
function rowAction(d: Department, key: string): void {
  if (key === 'sub') startAdding(d)
  else if (key === 'edit') startEditing(d)
  else if (key === 'toggle') void toggle(d)
  else if (key === 'delete') void remove(d)
}

function startEditing(d: Department): void {
  editing.value = { id: d.id, name: d.name, sort_order: String(d.sort_order) }
  error.value = null
}

async function saveEdit(): Promise<void> {
  const propertyId = pid.value
  const e = editing.value
  if (propertyId === null || e === null) return
  const ok = await run(async () => {
    await api.PATCH('/api/v1/properties/{propertyId}/departments/{id}', {
      params: { path: { propertyId, id: e.id } }, body: { name: e.name.trim(), sort_order: Number(e.sort_order) },
    })
    notice.value = t('departments.saved')
  })
  if (ok) editing.value = null
}

async function toggle(d: Department): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  await run(async () => {
    await api.PATCH('/api/v1/properties/{propertyId}/departments/{id}', { params: { path: { propertyId, id: d.id } }, body: { is_active: !d.is_active } })
    notice.value = t(d.is_active ? 'departments.switchedOff' : 'departments.switchedOn', { code: d.code })
  })
}

async function remove(d: Department): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  if (!(await confirm({ title: t('departments.deleteTitle', { code: d.code }), description: t('departments.deleteHint'), destructive: true }))) return
  await run(async () => {
    await api.DELETE('/api/v1/properties/{propertyId}/departments/{id}', { params: { path: { propertyId, id: d.id } } })
    notice.value = t('departments.deleted', { code: d.code })
  })
}

watch(() => pid.value, () => {
  departments.value = []
  setup.value = null
  loaded.value = false
  adding.value = false
  editing.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('departments.title')" :description="t('departments.intro')">
    <template #actions>
      <Button v-if="can('accounting.manage') && !adding" type="button" data-testid="add" @click="startAdding()">{{ t('departments.add') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline data-testid="department-error">
<template v-for="(f, i) in error.fieldErrors ?? []" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
</ErrorNotice>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('departments.noAccess', { permission: 'accounting.view' }) }}</p>
  <template v-else>
    <Card v-if="adding" class="mb-4">
      <form v-autofocus novalidate data-testid="add-form" @submit.prevent="create">
        <CardHeader><CardTitle>{{ form.parent_id ? t('departments.addSub') : t('departments.addTitle') }}</CardTitle></CardHeader>
        <CardContent class="flex flex-col gap-4">
          <div class="grid gap-4 sm:grid-cols-2">
            <FormField :label="t('departments.parent')" :hint="t('departments.parentHint')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.parent_id" name="parent_id">
                  <option value="">{{ t('departments.noParent') }}</option>
                  <option v-for="d in tops" :key="d.id" :value="String(d.id)">{{ d.code }} · {{ d.name }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('departments.code')" :hint="t('departments.codeHint')" required>
              <template #default="{ id }"><Input :id="id" v-model="form.code" data-autofocus name="code" maxlength="20" /></template>
            </FormField>
            <FormField :label="t('departments.name')" required>
              <template #default="{ id }"><Input :id="id" v-model="form.name" name="name" maxlength="100" /></template>
            </FormField>
            <FormField :label="t('departments.order')">
              <template #default="{ id }"><Input :id="id" v-model="form.sort_order" name="sort_order" inputmode="numeric" /></template>
            </FormField>
          </div>
          <div class="flex justify-end gap-2">
            <Button type="button" variant="outline" @click="adding = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || !form.code.trim() || !form.name.trim()" data-testid="create">{{ t('departments.create') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>
    <Card v-if="setup && setup.issues.length" class="mb-4" data-testid="setup-check">
      <CardHeader><CardTitle>{{ t('departments.setupTitle') }}</CardTitle></CardHeader>
      <CardContent class="flex flex-col gap-2 text-sm">
        <p class="m-0 text-muted-foreground">{{ setup.ok ? t('departments.setupWarnings') : t('departments.setupErrors') }}</p>
        <ul class="m-0 flex list-none flex-col gap-1 p-0">
          <li v-for="(i, n) in setup.issues" :key="n" class="flex items-start gap-2" :data-testid="`setup-issue-${i.severity}`">
            <Badge :variant="i.severity === 'ERROR' ? 'destructive' : 'outline'">{{ i.severity === 'ERROR' ? t('departments.setupError') : t('departments.setupWarning') }}</Badge>
            <span>{{ i.message }}</span>
          </li>
        </ul>
      </CardContent>
    </Card>
    <Card>
      <EmptyState v-if="loaded && !departments.length" :title="t('departments.empty')" data-testid="empty" />
      <div v-else class="overflow-x-auto">
      <table class="w-full border-collapse text-sm" data-testid="departments">
        <caption class="sr-only">{{ t('departments.title') }}</caption>
        <thead>
          <tr class="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
            <th class="px-3 py-2">{{ t('departments.code') }}</th>
            <th class="px-3 py-2">{{ t('departments.name') }}</th>
            <th class="px-3 py-2 text-right">{{ t('departments.order') }}</th>
            <th class="px-3 py-2">{{ t('departments.state') }}</th>
            <th class="px-3 py-2" />
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in departments" :key="d.id" class="border-b border-border" :data-testid="`department-${d.code}`" :data-level="d.level">
            <td class="px-3 py-2 tabular-nums" :class="d.level === 2 ? 'pl-8' : 'font-medium'">{{ d.code }}</td>
            <td class="px-3 py-2">
              <template v-if="editing?.id === d.id">
                <form class="flex items-center gap-2" novalidate :data-testid="`edit-form-${d.code}`" @submit.prevent="saveEdit">
                  <Input v-model="editing.name" name="edit_name" maxlength="100" class="h-8" :aria-label="t('departments.name')" />
                  <Input v-model="editing.sort_order" name="edit_order" inputmode="numeric" class="h-8 w-20" :aria-label="t('departments.order')" />
                  <Button type="submit" size="sm" :disabled="busy || !editing.name.trim()" data-testid="save-edit">{{ t('common.save') }}</Button>
                  <Button type="button" size="sm" variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
                </form>
              </template>
              <span v-else :class="d.level === 2 ? 'pl-4' : ''">{{ d.name }}</span>
            </td>
            <td class="px-3 py-2 text-right tabular-nums">{{ d.sort_order }}</td>
            <td class="px-3 py-2">
              <Badge :variant="d.is_active ? 'success' : 'outline'">{{ d.is_active ? t('departments.inUse') : t('departments.off') }}</Badge>
            </td>
            <td class="whitespace-nowrap px-3 py-2 text-right">
              <template v-if="can('accounting.manage')">
                <div class="hidden md:block">
                  <Button v-if="d.level === 1 && d.is_active" type="button" variant="ghost" size="sm" :data-testid="`sub-${d.code}`" @click="startAdding(d)">{{ t('departments.addSub') }}</Button>
                  <Button type="button" variant="ghost" size="sm" :data-testid="`edit-${d.code}`" @click="startEditing(d)">{{ t('common.edit') }}</Button>
                  <Button type="button" variant="ghost" size="sm" :disabled="busy" :data-testid="`toggle-${d.code}`" @click="toggle(d)">{{ d.is_active ? t('departments.switchOff') : t('departments.switchOn') }}</Button>
                  <Button v-if="!d.in_use && d.child_count === 0" type="button" variant="ghost" size="sm" class="text-destructive" :data-testid="`delete-${d.code}`" @click="remove(d)">{{ t('common.delete') }}</Button>
                </div>
                <div class="md:hidden" :data-testid="`more-${d.code}`">
                  <RowMenu :items="rowItems(d)" @select="(key) => rowAction(d, key)" />
                </div>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
      </div>
    </Card>
  </template>
</template>
