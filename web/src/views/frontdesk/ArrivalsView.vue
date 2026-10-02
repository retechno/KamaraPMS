<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Arrival, CheckInResult } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CheckInPanel from './CheckInPanel.vue'

/**
 * The arrivals tab of the front desk: the rooms due today and, from "Check in", a side sheet with the check-in form,
 * so the list stays in view. The page header and the tabs belong to FrontDeskView.
 */
const emit = defineEmits<{ loaded: [count: number] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()

const rows = ref<Arrival[]>([])
const open = ref<number | null>(null)
const error = ref<ApiError | null>(null)
const loaded = ref(false)

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const canCheckIn = computed(() => auth.can('frontdesk.checkin', property.currentId))
const current = computed(() => rows.value.find((a) => a.reservation_room_id === open.value) ?? null)
const sheetOpen = computed({
  get: () => open.value !== null,
  set: (v: boolean) => {
    if (!v) open.value = null
  },
})

const columns = computed<Column<Arrival>[]>(() => [
  { key: 'confirmation_number', label: t('frontDesk.page.confirmation'), sortable: true },
  { key: 'guest_name', label: t('frontDesk.page.guest'), sortable: true },
  { key: 'room_type_code', label: t('frontDesk.page.roomType'), sortable: true },
  { key: 'room_number', label: t('frontDesk.page.room'), sortable: true },
  { key: 'departure_date', label: t('frontDesk.page.departure'), sortable: true, format: 'date' as const },
  { key: 'party', label: t('frontDesk.page.party') },
  { key: 'actions', label: '', align: 'right' },
])

async function load(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/arrivals', { params: { path: { propertyId }, query: {} } })
    rows.value = data?.data ?? []
    loaded.value = true
    emit('loaded', rows.value.length)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function checkedIn(result: CheckInResult): Promise<void> {
  open.value = null
  await router.push(`/stays/${result.stay.id}`)
}

watch(() => property.currentId, () => {
  rows.value = []
  loaded.value = false
  open.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('frontDesk.page.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('frontDesk.page.noAccessArrivals') }}</p>

  <template v-else>
    <DataTable
      :columns="columns"
      :rows="rows"
      row-key="reservation_room_id"
      :loading="!loaded"
      :row-test-id="(a) => `arrival-${a.reservation_room_id}`"
      :caption="t('frontDesk.page.arrivals')"
    >
      <template #cell-confirmation_number="{ row }">
        <RouterLink :to="`/reservations/${row.reservation_id}`">{{ row.confirmation_number }}</RouterLink>
      </template>
      <template #cell-guest_name="{ row }">{{ row.guest_name || '—' }}</template>
      <template #cell-room_number="{ row }">
        <span v-if="row.room_number" class="inline-flex items-center gap-1.5">
          {{ row.room_number }}
          <StatusBadge v-if="row.housekeeping_status" domain="housekeeping" :status="row.housekeeping_status" />
        </span>
        <template v-else>—</template>
      </template>
      <template #cell-party="{ row }">{{ row.adult_count }}+{{ row.child_count }}</template>
      <template #cell-actions="{ row }">
        <Button v-if="canCheckIn" size="sm" :data-testid="`open-${row.reservation_room_id}`" @click="open = open === row.reservation_room_id ? null : row.reservation_room_id">
          {{ t('frontDesk.page.checkIn') }}
        </Button>
      </template>
      <template #empty>
        <EmptyState :title="t('frontDesk.page.noArrivals')" data-testid="empty" />
      </template>
    </DataTable>

    <Sheet v-model:open="sheetOpen">
      <SheetContent v-if="current" class="p-6" data-testid="checkin-sheet">
        <SheetTitle class="text-lg">
          {{ t('frontDesk.checkIn.title', { guest: current.guest_name || current.confirmation_number, type: current.room_type_code }) }}
        </SheetTitle>
        <SheetDescription class="mt-1">
          {{ t('frontDesk.checkIn.subtitle', { confirmation: current.confirmation_number, arrival: current.arrival_date, departure: current.departure_date }) }}
        </SheetDescription>
        <CheckInPanel :key="current.reservation_room_id" :arrival="current" class="mt-4" @done="checkedIn" @cancel="open = null" />
      </SheetContent>
    </Sheet>
  </template>
</template>
