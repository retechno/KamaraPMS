<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BedType } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const beds = ref<BedType[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<BedType | 'new' | null>(null)

const canManage = computed(() => auth.can('room.manage', property.currentId))
const blank = () => ({ code: '', name: '', sort_order: 0, is_active: true })
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

const columns = computed<Column<BedType>[]>(() => [
  { key: 'code', label: t('bedTypes.code'), sortable: true, filter: 'text' as const },
  { key: 'name', label: t('bedTypes.name'), sortable: true, filter: 'text' as const },
  { key: 'sort_order', label: t('bedTypes.sortOrder'), align: 'right', sortable: true },
  { key: 'status', label: t('setup.status'), filter: 'select' as const, filterValue: (b: BedType) => (b.is_active ? t('setup.active') : t('setup.inactive')) },
  ...(canManage.value ? [{ key: 'actions', label: '', align: 'right' as const }] : []),
])

async function load(): Promise<void> {
  const propertyId = property.currentId
  beds.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/bed-types', { params: { path: { propertyId } } })
    beds.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, blank(), { sort_order: (beds.value.at(-1)?.sort_order ?? 0) + 10 })
  error.value = null
  editing.value = 'new'
}

function startEdit(b: BedType): void {
  Object.assign(form, { code: b.code, name: b.name, sort_order: b.sort_order, is_active: b.is_active })
  error.value = null
  editing.value = b
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  const sort_order = Number(form.sort_order)
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/bed-types', {
        params: { path: { propertyId } },
        body: { code: form.code, name: form.name, sort_order, is_active: form.is_active },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/bed-types/{id}', {
        params: { path: { propertyId, id: editing.value.id } },
        body: { name: form.name, sort_order, is_active: form.is_active },
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

watch(() => property.currentId, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('bedTypes.title')" :description="t('bedTypes.intro')">
    <template #actions>
      <Button v-if="canManage && !editing" type="button" data-testid="new-bed" @click="startNew">{{ t('bedTypes.new') }}</Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <Card v-if="editing" class="mb-4">
    <form novalidate data-testid="bed-form" @submit.prevent="save">
      <CardHeader><CardTitle>{{ editing === 'new' ? t('bedTypes.new') : t('bedTypes.edit', { code: form.code }) }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <FormField :label="t('bedTypes.code')" :hint="t('bedTypes.codeHint')" :error="fieldError('code')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('bedTypes.name')" :error="fieldError('name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" maxlength="60" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('bedTypes.sortOrder')" :error="fieldError('sort_order')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.sort_order" name="sort_order" type="number" :aria-invalid="invalid" /></template>
          </FormField>
          <label class="flex items-center gap-2 self-end pb-2 text-sm">
            <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" />
            <span>{{ t('bedTypes.activeCheck') }}</span>
          </label>
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="saving">{{ t('common.save') }}</Button>
        </div>
      </CardContent>
    </form>
  </Card>

  <Card>
    <EmptyState v-if="loaded && !beds.length" :title="t('bedTypes.empty')" data-testid="empty" />
    <DataTable v-else-if="beds.length" :columns="columns" :rows="beds" row-key="id" :row-test-id="(b) => `bed-${b.code}`" :row-class="(b) => (b.is_active ? undefined : 'text-muted-foreground')" :caption="t('bedTypes.title')">
      <template #cell-code="{ row }"><b>{{ row.code }}</b></template>
      <template #cell-status="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('setup.active') : t('setup.inactive') }}</Badge></template>
      <template #cell-actions="{ row }"><Button type="button" variant="outline" size="sm" @click="startEdit(row)">{{ t('common.edit') }}</Button></template>
    </DataTable>
  </Card>
</template>
