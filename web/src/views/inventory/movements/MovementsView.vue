<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryRequisitionBrief, InventoryTransferBrief } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { postedVariant, requisitionVariant } from '@/utils/movements'
import { stageText } from '@/utils/purchasing'

/**
 * Requisitions and transfers (docs/architecture/35-inventory.md 5.4, mockup 22): the store requisitions (SR) with the stage of their approval and what answered them, and the transfers between warehouses, in
 * two tabs of one list page. A document opens its own page; "Transfer bebas" is a transfer that answers no requisition.
 */
const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)

type Tab = 'requisitions' | 'transfers'
const tab = computed<Tab>(() => (route.query.tab === 'transfers' ? 'transfers' : 'requisitions'))
const chip = ref('all')
const q = ref('')

const requisitions = ref<InventoryRequisitionBrief[]>([])
const transfers = ref<InventoryTransferBrief[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  const path = { params: { path: { propertyId }, query: { limit: 200 } } }
  try {
    const [r, x] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/inventory/requisitions', path),
      api.GET('/api/v1/properties/{propertyId}/inventory/transfers', path),
    ])
    requisitions.value = r.data?.data ?? []
    transfers.value = x.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })
watch(tab, () => {
  chip.value = 'all'
})

function go(next: Tab): void {
  void router.replace({ path: '/inventory/requisitions', query: next === 'requisitions' ? {} : { tab: next } })
}

const matches = (text: string): boolean => !q.value.trim() || text.toLowerCase().includes(q.value.trim().toLowerCase())
const waiting = computed(() => requisitions.value.filter((r) => r.waiting_for_me).length)
const chips = computed<{ key: string; label: string }[]>(() =>
  tab.value === 'requisitions'
    ? [
        { key: 'all', label: t('inventory.movements.chipAll') },
        { key: 'ready', label: t('inventory.movements.chipReady', { n: requisitions.value.filter((r) => r.status === 'APPROVED').length }) },
        { key: 'mine', label: t('inventory.movements.chipWaiting', { n: waiting.value }) },
        { key: 'PARTIAL', label: t('inventory.movements.chipPartial', { n: requisitions.value.filter((r) => r.status === 'PARTIAL').length }) },
        { key: 'FULFILLED', label: t('inventory.movements.chipFulfilled') },
        { key: 'REJECTED', label: t('inventory.movements.chipRejected') },
      ]
    : [
        { key: 'all', label: t('inventory.movements.chipAll') },
        { key: 'free', label: t('inventory.movements.chipFree') },
        { key: 'REVERSED', label: t('inventory.movements.chipReversed') },
      ],
)
const shownRequisitions = computed(() =>
  requisitions.value.filter(
    (r) => (chip.value === 'all' ? true : chip.value === 'mine' ? r.waiting_for_me : chip.value === 'ready' ? r.status === 'APPROVED' : r.status === chip.value) && matches(`${r.sr_number} ${r.requester_name} ${r.to_warehouse_name} ${r.department_name ?? ''}`),
  ),
)
const shownTransfers = computed(() => transfers.value.filter((x) => (chip.value === 'all' ? true : chip.value === 'free' ? x.requisition_id === null : x.status === chip.value) && matches(`${x.trf_number} ${x.from_warehouse_name} ${x.to_warehouse_name} ${x.requisition_number ?? ''}`)))

const srCols = computed<Column<InventoryRequisitionBrief>[]>(() => [
  { key: 'sr_number', label: t('inventory.movements.number'), class: 'font-medium whitespace-nowrap' },
  { key: 'to_warehouse_name', label: t('inventory.movements.askingWarehouse') },
  { key: 'from_warehouse_name', label: t('inventory.movements.fromWarehouse') },
  { key: 'requester_name', label: t('inventory.movements.requester') },
  { key: 'line_count', label: t('inventory.movements.items'), align: 'right' },
  { key: 'needed_on', label: t('inventory.movements.neededOn'), format: 'date' },
  { key: 'stage', label: t('inventory.movements.approval') },
  { key: 'answered_by', label: t('inventory.movements.answeredBy') },
  { key: 'status', label: t('inventory.movements.status') },
])
const trfCols = computed<Column<InventoryTransferBrief>[]>(() => [
  { key: 'trf_number', label: t('inventory.movements.number'), class: 'font-medium whitespace-nowrap' },
  { key: 'from_warehouse_name', label: t('inventory.movements.fromWarehouse') },
  { key: 'to_warehouse_name', label: t('inventory.movements.toWarehouse') },
  { key: 'total_value', label: t('inventory.movements.value'), align: 'right', format: 'money' },
  { key: 'business_date', label: t('inventory.movements.date'), format: 'date' },
  { key: 'requisition_number', label: t('inventory.movements.answers') },
  { key: 'status', label: t('inventory.movements.status') },
])
</script>

<template>
  <PageHeader :title="t('inventory.movements.title')" :description="t('inventory.movements.intro')">
    <template #actions>
      <Button v-if="can('inventory.move')" as-child variant="outline"><RouterLink to="/inventory/transfers/new" data-testid="transfer-free">{{ t('inventory.movements.freeTransfer') }}</RouterLink></Button>
      <Button v-if="can('inventory.request')" as-child><RouterLink to="/inventory/requisitions/new" data-testid="requisition-new">{{ t('inventory.movements.newRequisition') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <template v-else>
    <div class="mb-3 flex flex-wrap items-center gap-2" role="tablist" data-testid="movements-tabs">
      <Button v-for="x in ([['requisitions', requisitions.length], ['transfers', transfers.length]] as [Tab, number][])" :key="x[0]" type="button" size="sm" role="tab" :aria-selected="tab === x[0]"
        :variant="tab === x[0] ? 'default' : 'outline'" :data-testid="`tab-${x[0]}`" @click="go(x[0])">{{ t(`inventory.movements.tab.${x[0]}`) }} · {{ x[1] }}</Button>
    </div>
    <div class="mb-3 flex flex-wrap items-center gap-2">
      <input v-model="q" type="search" name="q" class="h-9 w-full max-w-sm rounded-md border border-border bg-card px-3 text-sm" :placeholder="t('inventory.movements.search')" :aria-label="t('inventory.movements.search')" data-testid="movements-search" />
      <Button v-for="c in chips" :key="c.key" type="button" size="sm" class="rounded-full" :variant="chip === c.key ? 'default' : 'outline'" :data-testid="`chip-${c.key}`" @click="chip = c.key">{{ c.label }}</Button>
    </div>

    <DataTable v-if="tab === 'requisitions'" :columns="srCols" :rows="shownRequisitions" row-key="id" :loading="!loaded" :empty-title="t('inventory.movements.emptyRequisitions')"
      :empty-description="t('emptyState.inventoryMovements')" :row-to="(r) => `/inventory/requisitions/${r.id}`" :row-test-id="(r) => `requisition-${r.sr_number}`" data-testid="requisitions-table">
      <template #cell-sr_number="{ row }"><RouterLink :to="`/inventory/requisitions/${row.id}`" class="text-primary hover:underline">{{ row.sr_number }}</RouterLink></template>
      <template #cell-stage="{ row }">{{ stageText(row.stage, row.status === 'APPROVED' || row.status === 'PARTIAL' || row.status === 'FULFILLED' ? 'APPROVED' : row.status) }}</template>
      <template #cell-answered_by="{ row }">{{ row.answered_by || '—' }}</template>
      <template #cell-status="{ row }"><Badge :variant="requisitionVariant(row.status)">{{ t(`inventory.requisitionStatus.${row.status}`) }}</Badge> <Badge v-if="row.waiting_for_me" variant="warning">{{ t('inventory.purchasing.waitingForMe') }}</Badge></template>
    </DataTable>
    <DataTable v-else :columns="trfCols" :rows="shownTransfers" row-key="id" :loading="!loaded" :empty-title="t('inventory.movements.emptyTransfers')" :empty-description="t('emptyState.inventoryMovements')"
      :row-to="(r) => `/inventory/transfers/${r.id}`" :row-test-id="(r) => `transfer-${r.trf_number}`" data-testid="transfers-table">
      <template #cell-trf_number="{ row }"><RouterLink :to="`/inventory/transfers/${row.id}`" class="text-primary hover:underline">{{ row.trf_number }}</RouterLink></template>
      <template #cell-requisition_number="{ row }">{{ row.requisition_number ?? t('inventory.movements.noRequisition') }}</template>
      <template #cell-status="{ row }"><Badge :variant="postedVariant(row.status)">{{ t(`inventory.postedStatus.${row.status}`) }}</Badge></template>
    </DataTable>
    <p v-if="loaded && tab === 'transfers'" class="muted mt-3 text-xs">{{ t('inventory.movements.transferHint') }}</p>
  </template>
</template>
