<script setup lang="ts">
import { CalendarPlus, GanttChart, Search } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { ReservationSummary } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const filter = reactive({ q: '', status: '', arrivalFrom: '', arrivalTo: '' })
const rows = ref<ReservationSummary[]>([])
const nextCursor = ref<string | undefined>()
const loading = ref(false)
const searched = ref(false)
const error = ref<ApiError | null>(null)

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const canCreate = computed(() => auth.can('reservation.create', property.currentId))

const columns = computed<Column<ReservationSummary>[]>(() => [
  { key: 'confirmation_number', label: t('reservations.confirmation'), sortable: true },
  { key: 'guest_name', label: t('reservations.booker'), sortable: true },
  { key: 'company', label: t('reservations.companyGroup') },
  { key: 'arrival_date', label: t('reservations.arrival'), sortable: true },
  { key: 'departure_date', label: t('reservations.departure'), sortable: true },
  { key: 'room_count', label: t('reservations.rooms'), align: 'right', sortable: true },
  { key: 'status', label: t('reservations.status'), sortable: true },
])

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations', {
      params: {
        path: { propertyId },
        query: {
          limit: 50,
          cursor: more ? nextCursor.value : undefined,
          q: filter.q.trim() || undefined,
          status: (filter.status || undefined) as 'DRAFT' | 'CONFIRMED' | 'CANCELLED' | undefined,
          arrival_from: filter.arrivalFrom || undefined,
          arrival_to: filter.arrivalTo || undefined,
        },
      },
    })
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
    searched.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

watch(() => property.currentId, () => {
  rows.value = []
  searched.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('reservations.title')">
    <template #actions>
      <Button as-child variant="outline" size="sm">
        <RouterLink to="/reservations/tape"><GanttChart />{{ t('reservations.tapeChart') }}</RouterLink>
      </Button>
      <Button v-if="canCreate" as-child size="sm" data-testid="new">
        <RouterLink to="/reservations/new"><CalendarPlus />{{ t('reservations.newReservation') }}</RouterLink>
      </Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('reservations.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('reservations.noAccess') }}</p>

  <template v-else>
    <form class="mb-4 flex flex-wrap items-end gap-3" role="search" novalidate @submit.prevent="load()">
      <FormField class="min-w-52 flex-1" :label="t('reservations.search')">
        <template #default="{ id }"><Input :id="id" v-model="filter.q" name="q" type="search" :placeholder="t('reservations.searchPlaceholder')" /></template>
      </FormField>
      <FormField class="w-40" :label="t('reservations.status')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="filter.status" name="status">
            <option value="">{{ t('reservations.any') }}</option>
            <option value="DRAFT">{{ t('status.DRAFT') }}</option>
            <option value="CONFIRMED">{{ t('status.CONFIRMED') }}</option>
            <option value="CANCELLED">{{ t('status.CANCELLED') }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField class="w-40" :label="t('reservations.arrivalFrom')">
        <template #default="{ id }"><Input :id="id" v-model="filter.arrivalFrom" name="arrival_from" type="date" /></template>
      </FormField>
      <FormField class="w-40" :label="t('reservations.arrivalTo')">
        <template #default="{ id }"><Input :id="id" v-model="filter.arrivalTo" name="arrival_to" type="date" /></template>
      </FormField>
      <Button type="submit" :disabled="loading"><Search />{{ t('reservations.search') }}</Button>
    </form>

    <DataTable :columns="columns" :rows="rows" row-key="id" :loading="!searched" :row-test-id="(r) => `res-${r.confirmation_number}`" :caption="t('reservations.title')">
      <template #cell-confirmation_number="{ row }"><RouterLink :to="`/reservations/${row.id}`">{{ row.confirmation_number }}</RouterLink></template>
      <template #cell-guest_name="{ row }">{{ row.guest_name || '—' }}</template>
      <template #cell-company="{ row }">{{ [row.company_name, row.group_code].filter(Boolean).join(' · ') || '—' }}</template>
      <template #cell-status="{ row }"><StatusBadge domain="reservation" :status="row.status" /></template>
      <template #empty><EmptyState :title="t('reservations.empty')" data-testid="empty" /></template>
      <template #footer>
        <div v-if="nextCursor" class="flex justify-center p-3">
          <Button variant="outline" size="sm" :disabled="loading" data-testid="more" @click="load(true)">{{ t('reservations.loadMore') }}</Button>
        </div>
      </template>
    </DataTable>
  </template>
</template>
