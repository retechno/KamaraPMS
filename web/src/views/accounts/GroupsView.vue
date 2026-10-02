<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Company, Group } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const groups = ref<Group[]>([])
const companies = ref<Company[]>([])
const nextCursor = ref<string | undefined>()
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const creating = ref(false)
const activeOnly = ref(true)
const missingDates = ref(false)

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const canManage = computed(() => auth.can('group.manage', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')
const columns = computed<Column<Group>[]>(() => [
  { key: 'code', label: t('groups.code') },
  { key: 'name', label: t('groups.name') },
  { key: 'company_name', label: t('groups.company') },
  { key: 'dates', label: t('groups.dates') },
  { key: 'reservation_count', label: t('groups.reservations'), align: 'right' },
  { key: 'room_count', label: t('groups.rooms'), align: 'right' },
  { key: 'status', label: t('setup.status') },
])
const blank = () => ({ code: '', name: '', company_id: 0, contact_name: '', contact_email: '', contact_phone: '', arrival_date: businessDate.value, departure_date: '', notes: '' })
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/groups', {
      params: { path: { propertyId }, query: { limit: 50, cursor: more ? nextCursor.value : undefined, active: activeOnly.value ? true : undefined } },
    })
    groups.value = more ? [...groups.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function loadCompanies(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  try {
    companies.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/companies', { params: { path: { propertyId }, query: { limit: 200, cursor, active: true } } }))
  } catch {
    companies.value = [] // a role that cannot see companies can still make a group without one
  }
}

function startNew(): void {
  Object.assign(form, blank())
  error.value = null
  missingDates.value = false
  creating.value = true
  void loadCompanies()
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  error.value = null
  // An empty date cannot be sent (the server cannot read "" as a date): ask for it here.
  if (!form.arrival_date || !form.departure_date) {
    missingDates.value = true
    return
  }
  missingDates.value = false
  saving.value = true
  try {
    await api.POST('/api/v1/properties/{propertyId}/groups', {
      params: { path: { propertyId } },
      body: {
        code: form.code, name: form.name, company_id: form.company_id || null, contact_name: form.contact_name, contact_email: form.contact_email,
        contact_phone: form.contact_phone, arrival_date: form.arrival_date, departure_date: form.departure_date, notes: form.notes, is_active: true,
      },
    })
    creating.value = false
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => property.currentId, () => {
  groups.value = []
  loaded.value = false
  void load()
}, { immediate: true })
watch(activeOnly, () => void load())
</script>

<template>
  <PageHeader :title="t('groups.title')">
    <template #actions>
      <Button v-if="canManage && !creating" type="button" data-testid="new-group" @click="startNew">{{ t('groups.new') }}</Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('groups.noAccess') }}</p>

  <Card v-if="creating" class="mb-4">
    <form novalidate data-testid="group-form" @submit.prevent="save">
      <CardHeader><CardTitle>{{ t('groups.new') }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <FormField :label="t('groups.code')" :error="fieldError('code')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('groups.name')" :error="fieldError('name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('groups.billedCompany')">
            <template #default="{ id }">
              <Combobox :id="id" v-model="form.company_id" name="company_id" :options="[{ value: 0, label: `${t('groups.noCompany')}` }, ...companies.map((c) => ({ value: c.id, label: `${c.code} · ${c.name}` }))]" />
            </template>
          </FormField>
          <FormField :label="t('groups.arrival')" :error="fieldError('arrival_date')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.arrival_date" name="arrival_date" type="date" :min="businessDate" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('groups.departure')" :error="fieldError('departure_date')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.departure_date" name="departure_date" type="date" :min="form.arrival_date" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('groups.contactName')">
            <template #default="{ id }"><Input :id="id" v-model="form.contact_name" name="contact_name" /></template>
          </FormField>
          <FormField :label="t('groups.contactEmail')" :error="fieldError('contact_email')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.contact_email" name="contact_email" type="email" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('groups.contactPhone')">
            <template #default="{ id }"><Input :id="id" v-model="form.contact_phone" name="contact_phone" /></template>
          </FormField>
          <FormField :label="t('groups.notes')">
            <template #default="{ id }"><Input :id="id" v-model="form.notes" name="notes" /></template>
          </FormField>
        </div>
        <p v-if="missingDates" class="mb-0 mt-3 text-xs text-destructive" role="alert" data-testid="dates-required">{{ t('groups.datesRequired') }}</p>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="creating = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="saving">{{ t('common.save') }}</Button>
        </div>
      </CardContent>
    </form>
  </Card>

  <Card v-if="canRead">
    <CardContent class="pt-4">
      <label class="mb-3 flex items-center gap-2 text-sm">
        <input v-model="activeOnly" type="checkbox" name="active_only" class="size-4 accent-primary" />
        <span>{{ t('groups.activeOnly') }}</span>
      </label>
      <EmptyState v-if="loaded && !groups.length" :title="t('groups.empty')" data-testid="empty" />
      <DataTable v-else-if="groups.length" :columns="columns" :rows="groups" row-key="id" :row-test-id="(g) => `group-${g.code}`" :caption="t('groups.title')">
        <template #cell-code="{ row }"><RouterLink :to="`/groups/${row.id}`" class="text-primary hover:underline"><b>{{ row.code }}</b></RouterLink></template>
        <template #cell-dates="{ row }">{{ t('groups.dateRange', { from: $date(row.arrival_date), to: $date(row.departure_date) }) }}</template>
        <template #cell-status="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('setup.active') : t('setup.inactive') }}</Badge></template>
      </DataTable>
      <div v-if="nextCursor" class="mt-3 flex justify-center">
        <Button type="button" variant="outline" data-testid="more" @click="load(true)">{{ t('groups.more') }}</Button>
      </div>
    </CardContent>
  </Card>
</template>
