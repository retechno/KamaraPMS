<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Group, GroupMember } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { statusLabel } from '@/utils/reservations'

const props = defineProps<{ id: string }>()
const auth = useAuthStore()
const property = usePropertyStore()

const group = ref<Group | null>(null)
const members = ref<GroupMember[]>([])
const error = ref<ApiError | null>(null)
const editing = ref(false)
const saving = ref(false)
const form = reactive({ name: '', arrival_date: '', departure_date: '', contact_name: '', contact_email: '', contact_phone: '', notes: '', is_active: true })

const columns = computed<Column<GroupMember>[]>(() => [
  { key: 'confirmation_number', label: t('groupDetail.confirmation') },
  { key: 'guest_name', label: t('groupDetail.booker') },
  { key: 'dates', label: t('groupDetail.dates') },
  { key: 'room_count', label: t('groupDetail.rooms'), align: 'right' },
  { key: 'status', label: t('setup.status') },
])

const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('group.manage', pid.value))
const canBook = computed(() => auth.can('reservation.create', pid.value) && !!group.value?.is_active)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const path = () => ({ path: { propertyId: pid.value as number, id: Number(props.id) } })

async function load(): Promise<void> {
  if (pid.value === null || !auth.can('reservation.read', pid.value)) return
  error.value = null
  try {
    const [g, m] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/groups/{id}', { params: path() }),
      api.GET('/api/v1/properties/{propertyId}/groups/{id}/reservations', { params: path() }),
    ])
    group.value = g.data ?? null
    members.value = m.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function startEdit(): void {
  if (!group.value) return
  const g = group.value
  Object.assign(form, {
    name: g.name, arrival_date: g.arrival_date, departure_date: g.departure_date, contact_name: g.contact_name ?? '', contact_email: g.contact_email ?? '',
    contact_phone: g.contact_phone ?? '', notes: g.notes ?? '', is_active: g.is_active,
  })
  error.value = null
  editing.value = true
}

async function save(): Promise<void> {
  saving.value = true
  error.value = null
  try {
    await api.PATCH('/api/v1/properties/{propertyId}/groups/{id}', { params: path(), body: { ...form } })
    editing.value = false
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => [pid.value, props.id], () => void load(), { immediate: true })
</script>

<template>
  <PageHeader :title="group ? `${group.code} · ${group.name}` : t('groupDetail.fallback')">
    <template #actions><RouterLink to="/groups" class="text-sm text-primary hover:underline">{{ t('groupDetail.back') }}</RouterLink></template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <span v-if="error.code === 'GROUP_HAS_ROOMS_OUTSIDE_DATES'"> {{ t('groupDetail.moveRooms') }}</span>
  </p>

  <Card v-if="group && !editing" class="mb-4" data-testid="group-summary">
    <CardContent class="pt-4">
      <dl class="m-0 grid gap-x-6 gap-y-3 sm:grid-cols-2 lg:grid-cols-3">
        <div><dt class="text-xs text-muted-foreground">{{ t('groupDetail.dates') }}</dt><dd class="m-0 text-sm">{{ t('groupDetail.dateRange', { from: group.arrival_date, to: group.departure_date }) }}</dd></div>
        <div><dt class="text-xs text-muted-foreground">{{ t('groupDetail.companyBilled') }}</dt><dd class="m-0 text-sm">{{ group.company_name || t('groupDetail.noCompany') }}</dd></div>
        <div><dt class="text-xs text-muted-foreground">{{ t('groupDetail.contact') }}</dt><dd class="m-0 text-sm">{{ group.contact_name }} {{ group.contact_email }} {{ group.contact_phone }}</dd></div>
        <div><dt class="text-xs text-muted-foreground">{{ t('groupDetail.reservations') }}</dt><dd class="m-0 text-sm">{{ t('groupDetail.reservationsValue', { n: group.reservation_count, rooms: group.room_count }) }}</dd></div>
        <div><dt class="text-xs text-muted-foreground">{{ t('groupDetail.status') }}</dt><dd class="m-0 text-sm">{{ group.is_active ? t('setup.active') : t('groupDetail.inactiveNote') }}</dd></div>
      </dl>
      <p v-if="group.notes" class="mb-0 mt-3 text-sm text-muted-foreground">{{ group.notes }}</p>
      <div class="mt-4 flex justify-end gap-2">
        <Button v-if="canManage" type="button" variant="outline" data-testid="edit-group" @click="startEdit">{{ t('common.edit') }}</Button>
        <RouterLink v-if="canBook" :to="{ path: '/reservations/new', query: { group: group.id } }" data-testid="add-rooms">
          <Button type="button" tabindex="-1">{{ t('groupDetail.addRooms') }}</Button>
        </RouterLink>
      </div>
    </CardContent>
  </Card>

  <Card v-if="group && editing" class="mb-4">
    <form novalidate data-testid="group-form" @submit.prevent="save">
      <CardHeader><CardTitle>{{ t('groupDetail.edit', { code: group.code }) }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <FormField :label="t('groupDetail.name')" :error="fieldError('name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('groupDetail.arrival')" :error="fieldError('arrival_date')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.arrival_date" name="arrival_date" type="date" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('groupDetail.departure')" :error="fieldError('departure_date')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.departure_date" name="departure_date" type="date" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('groupDetail.contactName')">
            <template #default="{ id }"><Input :id="id" v-model="form.contact_name" name="contact_name" /></template>
          </FormField>
          <FormField :label="t('groupDetail.contactEmail')" :error="fieldError('contact_email')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.contact_email" name="contact_email" type="email" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('groupDetail.contactPhone')">
            <template #default="{ id }"><Input :id="id" v-model="form.contact_phone" name="contact_phone" /></template>
          </FormField>
          <FormField :label="t('groupDetail.notes')">
            <template #default="{ id }"><Input :id="id" v-model="form.notes" name="notes" /></template>
          </FormField>
          <label class="flex items-center gap-2 self-end pb-2 text-sm">
            <input v-model="form.is_active" name="is_active" type="checkbox" class="size-4 accent-primary" />
            <span>{{ t('groupDetail.activeCheck') }}</span>
          </label>
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="editing = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="saving">{{ t('common.save') }}</Button>
        </div>
      </CardContent>
    </form>
  </Card>

  <Card v-if="group">
    <CardHeader><CardTitle>{{ t('groupDetail.members') }}</CardTitle></CardHeader>
    <CardContent>
      <p v-if="!members.length" class="m-0 text-sm text-muted-foreground" data-testid="no-members">{{ t('groupDetail.noMembers') }}</p>
      <DataTable v-else :columns="columns" :rows="members" row-key="reservation_id" :row-test-id="(m) => `member-${m.confirmation_number}`" :caption="t('groupDetail.members')">
        <template #cell-confirmation_number="{ row }"><RouterLink :to="`/reservations/${row.reservation_id}`" class="text-primary hover:underline">{{ row.confirmation_number }}</RouterLink></template>
        <template #cell-dates="{ row }">{{ t('groupDetail.dateRange', { from: row.arrival_date, to: row.departure_date }) }}</template>
        <template #cell-status="{ row }"><StatusBadge domain="reservation" :status="row.status" :label="statusLabel(row.status)" /></template>
      </DataTable>
    </CardContent>
  </Card>
</template>
