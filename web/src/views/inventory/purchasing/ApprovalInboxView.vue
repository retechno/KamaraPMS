<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryInboxItem } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDate } from '@/utils/format'

/** "Perlu saya setujui" (docs/architecture/35-inventory.md 5.8 item 10): the requests whose active level the signed-in user can decide now. */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const items = ref<InventoryInboxItem[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/approvals/inbox', { params: { path: { propertyId } } })
    items.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const columns = computed<Column<InventoryInboxItem>[]>(() => [
  { key: 'request_number', label: t('inventory.inbox.number'), class: 'font-medium whitespace-nowrap' },
  { key: 'department_name', label: t('inventory.inbox.department') },
  { key: 'requester_name', label: t('inventory.inbox.requester') },
  { key: 'submitted_at', label: t('inventory.inbox.submitted') },
  { key: 'est_total', label: t('inventory.inbox.estimate'), align: 'right', format: 'money' },
  { key: 'level', label: t('inventory.inbox.level') },
])
</script>

<template>
  <PageHeader :title="t('inventory.inbox.title')" :description="t('inventory.inbox.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <DataTable v-else :columns="columns" :rows="items" row-key="request_id" :loading="!loaded" :empty-title="t('inventory.inbox.empty')" :empty-description="t('emptyState.inventoryInbox')"
    :row-to="(r) => `/inventory/requests/${r.request_id}`" :row-test-id="(r) => `inbox-${r.request_number}`" data-testid="inbox-table">
    <template #cell-request_number="{ row }"><RouterLink :to="`/inventory/requests/${row.request_id}`" class="text-primary hover:underline">{{ row.request_number }}</RouterLink></template>
    <template #cell-department_name="{ row }">{{ row.department_name ?? '—' }}</template>
    <template #cell-submitted_at="{ row }">{{ row.submitted_at ? formatDate(row.submitted_at.slice(0, 10)) : '' }}</template>
    <template #cell-level="{ row }">{{ row.level_no }} · {{ row.step_name }}</template>
  </DataTable>
</template>
