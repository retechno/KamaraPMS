<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { RoomType } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const types = ref<RoomType[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<RoomType | 'new' | null>(null)

const canManage = computed(() => auth.can('room.manage', property.currentId))

const blank = () => ({
  code: '',
  name: '',
  description: '',
  max_adult: 2,
  max_child: 0,
  max_occupancy: 2,
  base_occupancy: 2,
  sort_order: 0,
  is_active: true,
})
const form = reactive(blank())

const sorted = computed(() => [...types.value].sort((a, b) => a.sort_order - b.sort_order || a.code.localeCompare(b.code)))
const columns = computed<Column<RoomType>[]>(() => [
  { key: 'code', label: t('roomTypes.code') },
  { key: 'name', label: t('roomTypes.name') },
  { key: 'adults', label: t('roomTypes.adultsChildren') },
  { key: 'occupancy', label: t('roomTypes.occupancy') },
  { key: 'status', label: t('setup.status') },
  ...(canManage.value ? [{ key: 'actions', label: '', align: 'right' as const }] : []),
])
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function load(): Promise<void> {
  const propertyId = property.currentId
  types.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    types.value = await fetchAll((cursor) =>
      api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }),
    )
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

function startEdit(t: RoomType): void {
  Object.assign(form, { ...blank(), ...t, description: t.description ?? '' })
  error.value = null
  editing.value = t
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  const capacity = {
    max_adult: Number(form.max_adult),
    max_child: Number(form.max_child),
    max_occupancy: Number(form.max_occupancy),
    base_occupancy: Number(form.base_occupancy),
    sort_order: Number(form.sort_order),
  }
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/room-types', {
        params: { path: { propertyId } },
        body: { code: form.code, name: form.name, description: form.description || undefined, is_active: form.is_active, ...capacity },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/room-types/{id}', {
        params: { path: { propertyId, id: editing.value.id } },
        body: { name: form.name, description: form.description, is_active: form.is_active, ...capacity },
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
  <PageHeader :title="t('roomTypes.title')">
    <template #actions>
      <Button v-if="canManage && !editing" type="button" data-testid="new-type" @click="startNew">{{ t('roomTypes.new') }}</Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <Card v-if="editing" class="mb-4">
    <form v-autofocus="editing" novalidate @submit.prevent="save">
      <CardHeader><CardTitle>{{ editing === 'new' ? t('roomTypes.new') : t('roomTypes.edit', { code: form.code }) }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <FormField :label="t('roomTypes.code')" :hint="t('roomTypes.codeHint')" :error="fieldError('code')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('roomTypes.name')" :error="fieldError('name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('roomTypes.description')">
            <template #default="{ id }"><Input :id="id" v-model="form.description" name="description" /></template>
          </FormField>
          <FormField :label="t('roomTypes.maxAdult')" :error="fieldError('max_adult')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.max_adult" name="max_adult" type="number" min="1" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('roomTypes.maxChild')" :error="fieldError('max_child')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.max_child" name="max_child" type="number" min="0" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('roomTypes.sortOrder')">
            <template #default="{ id }"><Input :id="id" v-model="form.sort_order" name="sort_order" type="number" /></template>
          </FormField>
          <FormField :label="t('roomTypes.maxOccupancy')" :hint="t('roomTypes.maxOccupancyHint')" :error="fieldError('max_occupancy')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.max_occupancy" name="max_occupancy" type="number" min="1" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('roomTypes.baseOccupancy')" :hint="t('roomTypes.baseOccupancyHint')" :error="fieldError('base_occupancy')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.base_occupancy" name="base_occupancy" type="number" min="1" :aria-invalid="invalid" /></template>
          </FormField>
          <label class="flex items-center gap-2 self-end pb-2 text-sm">
            <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" />
            <span>{{ t('roomTypes.activeCheck') }}</span>
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
    <EmptyState v-if="loaded && !types.length" :title="t('roomTypes.empty')" :description="t('roomTypes.emptyHint')" data-testid="empty" />
    <DataTable v-else-if="types.length" :columns="columns" :rows="sorted" row-key="id" :row-test-id="(r) => `type-${r.code}`" :caption="t('roomTypes.title')">
      <template #cell-code="{ row }"><b>{{ row.code }}</b></template>
      <template #cell-adults="{ row }">{{ row.max_adult }} / {{ row.max_child }}</template>
      <template #cell-occupancy="{ row }">{{ row.base_occupancy }} / {{ row.max_occupancy }}</template>
      <template #cell-status="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('setup.active') : t('setup.inactive') }}</Badge></template>
      <template #cell-actions="{ row }"><Button type="button" variant="outline" size="sm" @click="startEdit(row)">{{ t('common.edit') }}</Button></template>
    </DataTable>
  </Card>
</template>
