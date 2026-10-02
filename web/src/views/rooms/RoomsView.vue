<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { HousekeepingStatus, Room, RoomType } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const rooms = ref<Room[]>([])
const types = ref<RoomType[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<Room | 'new' | null>(null)
const typeFilter = ref('')

const canManage = computed(() => auth.can('room.manage', property.currentId))
const typeById = computed(() => new Map(types.value.map((t) => [t.id, t])))
const assignable = computed(() => types.value.filter((t) => t.is_active || (editing.value !== 'new' && editing.value?.room_type_id === t.id)))
const visible = computed(() => {
  const list = typeFilter.value ? rooms.value.filter((r) => r.room_type_id === Number(typeFilter.value)) : rooms.value
  return [...list].sort((a, b) => a.room_number.localeCompare(b.room_number, undefined, { numeric: true }))
})

const columns = computed<Column<Room>[]>(() => [
  { key: 'room_number', label: t('rooms.room') },
  { key: 'type', label: t('rooms.type') },
  { key: 'floor', label: t('rooms.floor') },
  { key: 'building', label: t('rooms.building') },
  { key: 'status', label: t('setup.status') },
  ...(canManage.value ? [{ key: 'actions', label: '', align: 'right' as const }] : []),
])

const blank = () => ({
  room_number: '',
  room_type_id: 0,
  floor: '',
  building: '',
  is_active: true,
  initial_housekeeping_status: 'DIRTY' as HousekeepingStatus,
})
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

/** Stays and reservations that block a type change or deactivation (from the ROOM_IN_USE problem). */
const conflicts = computed(() => {
  const c = error.value?.context.conflicts
  return Array.isArray(c) ? (c as { type: string; id: number; reference?: string; from: string; to: string }[]) : []
})

async function load(): Promise<void> {
  const propertyId = property.currentId
  rooms.value = []
  types.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    const [t, r] = await Promise.all([
      fetchAll((cursor) =>
        api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }),
      ),
      fetchAll((cursor) =>
        api.GET('/api/v1/properties/{propertyId}/rooms', { params: { path: { propertyId }, query: { limit: 200, cursor } } }),
      ),
    ])
    types.value = t
    rooms.value = r
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, blank(), { room_type_id: types.value.find((t) => t.is_active)?.id ?? 0 })
  error.value = null
  editing.value = 'new'
}

function startEdit(r: Room): void {
  Object.assign(form, blank(), { ...r, floor: r.floor ?? '', building: r.building ?? '' })
  error.value = null
  editing.value = r
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/rooms', {
        params: { path: { propertyId } },
        body: {
          room_number: form.room_number,
          room_type_id: Number(form.room_type_id),
          floor: form.floor || undefined,
          building: form.building || undefined,
          is_active: form.is_active,
          initial_housekeeping_status: form.initial_housekeeping_status,
        },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/rooms/{id}', {
        params: { path: { propertyId, id: editing.value.id } },
        body: {
          room_number: form.room_number,
          room_type_id: Number(form.room_type_id),
          floor: form.floor,
          building: form.building,
          is_active: form.is_active,
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

watch(() => property.currentId, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('rooms.title')">
    <template #actions>
      <Button v-if="canManage && !editing" type="button" :disabled="!types.length" data-testid="new-room" @click="startNew">{{ t('rooms.new') }}</Button>
    </template>
  </PageHeader>

  <div v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <ul v-if="conflicts.length" class="m-0 mt-1.5 pl-5" data-testid="conflicts">
      <li v-for="c in conflicts" :key="`${c.type}-${c.id}`">
        {{ c.type === 'STAY' ? t('rooms.stayConflict', { ref: c.reference ?? c.id }) : t('rooms.lineConflict', { id: c.id }) }}: {{ t('rooms.conflictRange', { from: $date(c.from), to: $date(c.to) }) }}
      </li>
    </ul>
  </div>
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="loaded && !types.length" class="muted">{{ t('rooms.needType') }}</p>

  <Card v-if="editing" class="mb-4">
    <form novalidate @submit.prevent="save">
      <CardHeader><CardTitle>{{ editing === 'new' ? t('rooms.new') : t('rooms.edit', { number: editing.room_number }) }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <FormField :label="t('rooms.roomNumber')" :error="fieldError('room_number')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.room_number" name="room_number" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('rooms.roomType')" :error="fieldError('room_type_id')">
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model="form.room_type_id" name="room_type_id" :aria-invalid="invalid">
                <option v-for="rt in assignable" :key="rt.id" :value="rt.id">{{ rt.code }} · {{ rt.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField v-if="editing === 'new'" :label="t('rooms.initialHousekeeping')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.initial_housekeeping_status" name="initial_housekeeping_status">
                <option value="DIRTY">{{ t('status.DIRTY') }}</option>
                <option value="CLEAN">{{ t('status.CLEAN') }}</option>
                <option value="INSPECTED">{{ t('status.INSPECTED') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('rooms.floor')">
            <template #default="{ id }"><Input :id="id" v-model="form.floor" name="floor" /></template>
          </FormField>
          <FormField :label="t('rooms.building')">
            <template #default="{ id }"><Input :id="id" v-model="form.building" name="building" /></template>
          </FormField>
          <label class="flex items-center gap-2 self-end pb-2 text-sm">
            <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" />
            <span>{{ t('rooms.activeCheck') }}</span>
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
    <CardContent class="pt-4">
      <FormField v-if="types.length > 1" class="mb-3 max-w-56" :label="t('rooms.roomType')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="typeFilter" name="type_filter">
            <option value="">{{ t('rooms.allTypes') }}</option>
            <option v-for="rt in types" :key="rt.id" :value="rt.id">{{ rt.code }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <EmptyState v-if="loaded && !rooms.length && types.length" :title="t('rooms.empty')" data-testid="empty" />
      <DataTable v-else-if="rooms.length" :columns="columns" :rows="visible" row-key="id" :row-test-id="(r) => `room-${r.room_number}`" :caption="t('rooms.title')">
        <template #cell-room_number="{ row }"><b>{{ row.room_number }}</b></template>
        <template #cell-type="{ row }">{{ typeById.get(row.room_type_id)?.code }}</template>
        <template #cell-status="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('setup.active') : t('setup.inactive') }}</Badge></template>
        <template #cell-actions="{ row }"><Button type="button" variant="outline" size="sm" @click="startEdit(row)">{{ t('common.edit') }}</Button></template>
      </DataTable>
    </CardContent>
  </Card>
</template>
