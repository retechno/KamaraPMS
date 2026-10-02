<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { StaySummary } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/** The departures tab of the front desk: open stays due to leave today or overdue. The header and tabs belong to FrontDeskView. */
const emit = defineEmits<{ loaded: [count: number, more: boolean] }>()

const auth = useAuthStore()
const property = usePropertyStore()

const rows = ref<StaySummary[]>([])
const nextCursor = ref<string | undefined>()
const error = ref<ApiError | null>(null)
const loading = ref(false)
const loaded = ref(false)

const businessDate = computed(() => property.clock?.business_date ?? '')
const canRead = computed(() => auth.can('reservation.read', property.currentId))
const canCheckOut = computed(() => auth.can('frontdesk.checkout', property.currentId))
const overdue = (s: StaySummary) => s.departure_date < businessDate.value

const columns = computed<Column<StaySummary>[]>(() => [
  { key: 'room_number', label: t('frontDesk.page.room'), sortable: true, filter: 'text' as const },
  { key: 'guest_name', label: t('frontDesk.page.guest'), sortable: true, filter: 'text' as const },
  { key: 'stay_number', label: t('frontDesk.page.stay'), sortable: true, filter: 'text' as const },
  { key: 'arrival_date', label: t('frontDesk.page.arrival'), sortable: true, format: 'date' as const },
  { key: 'departure_date', label: t('frontDesk.page.departure'), sortable: true, format: 'date' as const },
  { key: 'actions', label: '', align: 'right' },
])

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value || !businessDate.value) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/stays', {
      params: { path: { propertyId }, query: { status: 'OPEN', departure_until: businessDate.value, limit: 50, cursor: more ? nextCursor.value : undefined } },
    })
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
    loaded.value = true
    emit('loaded', rows.value.length, !!nextCursor.value)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

watch(() => [property.currentId, businessDate.value], () => {
  rows.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('frontDesk.page.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('frontDesk.page.noAccessStays') }}</p>

  <template v-else>
    <DataTable :columns="columns" :rows="rows" row-key="id" :loading="!loaded" :row-test-id="(s) => `stay-${s.stay_number}`" :caption="t('frontDesk.page.departures')">
      <template #cell-stay_number="{ row }"><RouterLink :to="`/stays/${row.id}`">{{ row.stay_number }}</RouterLink></template>
      <template #cell-departure_date="{ row }">
        {{ $date(row.departure_date) }}
        <Badge v-if="overdue(row)" variant="warning" class="ml-1" data-testid="overdue">{{ t('frontDesk.page.overdue') }}</Badge>
      </template>
      <template #cell-actions="{ row }">
        <Button v-if="canCheckOut" as-child size="sm" variant="outline" :data-testid="`checkout-${row.stay_number}`">
          <RouterLink :to="`/stays/${row.id}`">{{ t('frontDesk.page.checkOut') }}</RouterLink>
        </Button>
      </template>
      <template #empty><EmptyState :title="t('frontDesk.page.noDepartures')" data-testid="empty" /></template>
      <template #footer>
        <div v-if="nextCursor" class="flex justify-center p-3">
          <Button variant="outline" size="sm" :disabled="loading" data-testid="more" @click="load(true)">{{ t('frontDesk.page.loadMore') }}</Button>
        </div>
      </template>
    </DataTable>
  </template>
</template>
