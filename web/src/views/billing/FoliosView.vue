<script setup lang="ts">
import { usePagedList } from '@/composables/usePagedList'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Search, Wallet } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import type { FolioSummary } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { statusText } from '@/utils/status'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const status = ref('OPEN')

const canRead = computed(() => auth.can('folio.read', property.currentId))

const columns = computed<Column<FolioSummary>[]>(() => [
  { key: 'folio_number', label: t('folios.folio'), sortable: true, filter: 'text' as const, card: 'primary' as const },
  { key: 'reservation_id', label: t('folios.reservation'), sortable: true, filter: 'text' as const, card: 'secondary' as const },
  { key: 'status', label: t('folios.status'), sortable: true, filter: 'select' as const, filterValue: (r: FolioSummary) => statusText(r.status), card: 'badge' as const },
  { key: 'balance', label: t('folios.balance'), align: 'right', sortable: true, class: 'tabular-nums', format: 'money' as const, card: 'money' as const },
])

// One page of 50 at a time; the filter of the status is asked of the server, and a new search starts from the first page.
const list = usePagedList<FolioSummary>(async (cursor) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/folios', {
    params: { path: { propertyId: property.currentId! }, query: { limit: 50, cursor, status: (status.value || undefined) as 'OPEN' | 'CLOSED' | undefined } },
  })
  return { data: data?.data ?? [], next_cursor: data?.next_cursor }
})
const { rows, error, loading, loadingMore, loaded: searched, hasMore } = list

async function load(): Promise<void> {
  if (property.currentId === null || !canRead.value) return
  await list.reload()
}

watch(() => property.currentId, () => {
  list.reset()
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

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
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

    <DataTable
      :columns="columns"
      :rows="rows"
      row-key="id"
      :loading="!searched"
      :has-more="hasMore"
      :loading-more="loadingMore"
      cards
      :row-to="(f) => `/folios/${f.id}`"
      :row-test-id="(f) => `folio-${f.folio_number}`"
      :caption="t('folios.title')"
      @load-more="list.loadMore()"
    >
      <template #cell-folio_number="{ row }"><RouterLink :to="`/folios/${row.id}`">{{ row.folio_number }}</RouterLink></template>
      <template #cell-reservation_id="{ row }"><RouterLink :to="`/reservations/${row.reservation_id}`">#{{ row.reservation_id }}</RouterLink></template>
      <template #cell-status="{ row }"><StatusBadge domain="record" :status="row.status" /></template>
      <template #empty><EmptyState :title="t('folios.empty')" data-testid="empty" /></template>
    </DataTable>
  </template>
</template>
