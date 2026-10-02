<script setup lang="ts">
import { Search, Wallet } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { FolioSummary } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const status = ref('OPEN')
const rows = ref<FolioSummary[]>([])
const nextCursor = ref<string | undefined>()
const error = ref<ApiError | null>(null)
const loading = ref(false)
const searched = ref(false)

const canRead = computed(() => auth.can('folio.read', property.currentId))

const columns = computed<Column<FolioSummary>[]>(() => [
  { key: 'folio_number', label: t('folios.folio'), sortable: true },
  { key: 'reservation_id', label: t('folios.reservation'), sortable: true },
  { key: 'status', label: t('folios.status'), sortable: true },
  { key: 'balance', label: t('folios.balance'), align: 'right', sortable: true, class: 'tabular-nums', format: 'money' as const },
])

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/folios', {
      params: { path: { propertyId }, query: { limit: 50, cursor: more ? nextCursor.value : undefined, status: (status.value || undefined) as 'OPEN' | 'CLOSED' | undefined } },
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
  <PageHeader :title="t('folios.title')">
    <template #actions>
      <Button as-child variant="outline" size="sm">
        <RouterLink to="/cashier"><Wallet />{{ t('folios.cashierLink') }}</RouterLink>
      </Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('folios.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('folios.noAccess') }}</p>

  <template v-else>
    <form class="mb-4 flex flex-wrap items-end gap-3" role="search" @submit.prevent="load()">
      <FormField class="w-44" :label="t('folios.status')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="status" name="status">
            <option value="">{{ t('folios.any') }}</option>
            <option value="OPEN">{{ t('folios.open') }}</option>
            <option value="CLOSED">{{ t('folios.closed') }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <Button type="submit" :disabled="loading"><Search />{{ t('folios.search') }}</Button>
    </form>

    <DataTable :columns="columns" :rows="rows" row-key="id" :loading="!searched" :row-test-id="(f) => `folio-${f.folio_number}`" :caption="t('folios.title')">
      <template #cell-folio_number="{ row }"><RouterLink :to="`/folios/${row.id}`">{{ row.folio_number }}</RouterLink></template>
      <template #cell-reservation_id="{ row }"><RouterLink :to="`/reservations/${row.reservation_id}`">#{{ row.reservation_id }}</RouterLink></template>
      <template #cell-status="{ row }"><StatusBadge domain="record" :status="row.status" /></template>
      <template #empty><EmptyState :title="t('folios.empty')" data-testid="empty" /></template>
      <template #footer>
        <div v-if="nextCursor" class="flex justify-center p-3">
          <Button variant="outline" size="sm" :disabled="loading" data-testid="more" @click="load(true)">{{ t('folios.loadMore') }}</Button>
        </div>
      </template>
    </DataTable>
  </template>
</template>
