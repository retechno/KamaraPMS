<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryPOBrief, InventoryReceiptBrief, InventoryRequestBrief, InventoryReturnBrief, InventorySettings } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate } from '@/utils/format'
import { formatQty } from '@/utils/inventory'
import { draftVariant, orderVariant, requestVariant, stageText } from '@/utils/purchasing'

/**
 * Purchasing (docs/architecture/35-inventory.md 5.3, mockup 18): the purchase requests, the orders, the receipts and the returns in tabs of one list page, with the chip "waiting for my approval" and the
 * stage of each request in its chain. A document opens its own page.
 */
const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)

type Tab = 'requests' | 'orders' | 'receipts' | 'returns'
const tab = computed<Tab>(() => (['requests', 'orders', 'receipts', 'returns'].includes(String(route.query.tab)) ? (route.query.tab as Tab) : 'requests'))
const chip = ref('all')

const requests = ref<InventoryRequestBrief[]>([])
const orders = ref<InventoryPOBrief[]>([])
const receipts = ref<InventoryReceiptBrief[]>([])
const returns = ref<InventoryReturnBrief[]>([])
const settings = ref<InventorySettings | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const q = ref('')

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  const path = { params: { path: { propertyId }, query: { limit: 200 } } }
  try {
    const [r, o, g, x, s] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/inventory/requests', path),
      api.GET('/api/v1/properties/{propertyId}/inventory/purchase-orders', path),
      api.GET('/api/v1/properties/{propertyId}/inventory/receipts', path),
      api.GET('/api/v1/properties/{propertyId}/inventory/returns', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/inventory/settings', { params: { path: { propertyId } } }),
    ])
    requests.value = r.data?.data ?? []
    orders.value = o.data?.data ?? []
    receipts.value = g.data?.data ?? []
    returns.value = x.data?.data ?? []
    settings.value = s.data ?? null
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
  void router.replace({ path: '/inventory/purchasing', query: next === 'requests' ? {} : { tab: next } })
}

const needMine = computed(() => requests.value.filter((r) => r.waiting_for_me).length)
const chips = computed<{ key: string; label: string }[]>(() => {
  if (tab.value === 'requests') {
    return [
      { key: 'all', label: t('inventory.purchasing.chipAll') },
      { key: 'mine', label: t('inventory.purchasing.chipWaiting', { n: needMine.value }) },
      { key: 'DRAFT', label: t('inventory.purchasing.chipDraft', { n: requests.value.filter((r) => r.status === 'DRAFT').length }) },
      { key: 'APPROVED', label: t('inventory.purchasing.chipApproved') },
      { key: 'CONVERTED', label: t('inventory.purchasing.chipConverted') },
      { key: 'RETURNED', label: t('inventory.purchasing.chipReturned') },
    ]
  }
  if (tab.value === 'orders') {
    return [
      { key: 'all', label: t('inventory.purchasing.chipAll') },
      { key: 'open', label: t('inventory.purchasing.chipOpenOrders') },
      { key: 'RECEIVED', label: t('inventory.purchasing.chipReceived') },
    ]
  }
  if (tab.value === 'receipts') {
    return [
      { key: 'all', label: t('inventory.purchasing.chipAll') },
      { key: 'unbilled', label: t('inventory.purchasing.chipUnbilled') },
    ]
  }
  return []
})

const matches = (text: string): boolean => !q.value.trim() || text.toLowerCase().includes(q.value.trim().toLowerCase())
const shownRequests = computed(() =>
  requests.value.filter((r) => (chip.value === 'all' ? true : chip.value === 'mine' ? r.waiting_for_me : r.status === chip.value) && matches(`${r.request_number} ${r.requester_name} ${r.department_name ?? ''}`)),
)
const shownOrders = computed(() =>
  orders.value.filter((o) => (chip.value === 'all' ? true : chip.value === 'open' ? o.status === 'ORDERED' || o.status === 'PARTIAL' : o.status === chip.value) && matches(`${o.po_number} ${o.supplier_name}`)),
)
const shownReceipts = computed(() =>
  receipts.value.filter((r) => (chip.value === 'all' ? true : r.draft_status === 'OPEN' || r.draft_status === 'PARTIAL') && matches(`${r.receipt_number} ${r.supplier_name} ${r.delivery_note ?? ''}`)),
)

const requestCols = computed<Column<InventoryRequestBrief>[]>(() => [
  { key: 'request_number', label: t('inventory.purchasing.number'), class: 'font-medium whitespace-nowrap' },
  { key: 'department_name', label: t('inventory.purchasing.department') },
  { key: 'line_count', label: t('inventory.purchasing.lines'), align: 'right' },
  { key: 'requester_name', label: t('inventory.purchasing.requester') },
  { key: 'needed_on', label: t('inventory.purchasing.needed'), format: 'date' },
  { key: 'est_total', label: t('inventory.purchasing.estimate'), align: 'right', format: 'money' },
  { key: 'stage', label: t('inventory.purchasing.stage') },
  { key: 'status', label: t('inventory.purchasing.status') },
])
const orderCols = computed<Column<InventoryPOBrief>[]>(() => [
  { key: 'po_number', label: t('inventory.purchasing.number'), class: 'font-medium whitespace-nowrap' },
  { key: 'supplier_name', label: t('inventory.purchasing.supplier') },
  { key: 'warehouse_name', label: t('inventory.purchasing.warehouse') },
  { key: 'request_number', label: t('inventory.purchasing.fromRequest') },
  { key: 'order_date', label: t('inventory.purchasing.orderDate'), format: 'date' },
  { key: 'received', label: t('inventory.purchasing.received'), align: 'right' },
  { key: 'total', label: t('inventory.purchasing.value'), align: 'right', format: 'money' },
  { key: 'status', label: t('inventory.purchasing.status') },
])
const receiptCols = computed<Column<InventoryReceiptBrief>[]>(() => [
  { key: 'receipt_number', label: t('inventory.purchasing.number'), class: 'font-medium whitespace-nowrap' },
  { key: 'supplier_name', label: t('inventory.purchasing.supplier') },
  { key: 'warehouse_name', label: t('inventory.purchasing.warehouse') },
  { key: 'po_number', label: t('inventory.purchasing.order') },
  { key: 'received_on', label: t('inventory.purchasing.receivedOn'), format: 'date' },
  { key: 'total_value', label: t('inventory.purchasing.value'), align: 'right', format: 'money' },
  { key: 'draft', label: t('inventory.purchasing.bill') },
  { key: 'status', label: t('inventory.purchasing.status') },
])
const returnCols = computed<Column<InventoryReturnBrief>[]>(() => [
  { key: 'return_number', label: t('inventory.purchasing.number'), class: 'font-medium whitespace-nowrap' },
  { key: 'supplier_name', label: t('inventory.purchasing.supplier') },
  { key: 'receipt_number', label: t('inventory.purchasing.receipt') },
  { key: 'reason', label: t('inventory.purchasing.reason') },
  { key: 'total_value', label: t('inventory.purchasing.value'), align: 'right', format: 'money' },
  { key: 'credit', label: t('inventory.purchasing.creditNote') },
])
const receivedText = (o: InventoryPOBrief): string => (Number(o.qty_ordered) === 0 ? '' : `${formatQty(o.qty_received)} / ${formatQty(o.qty_ordered)}`)
const canDirect = computed(() => can('inventory.receive') && settings.value?.direct_receipt_limit != null)
</script>

<template>
  <PageHeader :title="t('inventory.purchasing.title')" :description="t('inventory.purchasing.intro')">
    <template #actions>
      <Button v-if="canDirect" as-child variant="outline">
        <RouterLink to="/inventory/receipts/new" data-testid="receipt-direct">{{ t('inventory.purchasing.directReceipt') }}</RouterLink>
      </Button>
      <Button v-if="can('inventory.purchase') && settings && !settings.po_requires_pr" as-child variant="outline"><RouterLink to="/inventory/purchase-orders/new" data-testid="order-new">{{ t('inventory.purchasing.newOrder') }}</RouterLink></Button>
      <Button v-if="can('inventory.request')" as-child><RouterLink to="/inventory/requests/new" data-testid="request-new">{{ t('inventory.purchasing.newRequest') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <template v-else>
    <div class="mb-3 flex flex-wrap items-center gap-2" role="tablist" data-testid="purchasing-tabs">
      <Button v-for="x in ([['requests', requests.length], ['orders', orders.length], ['receipts', receipts.length], ['returns', returns.length]] as [Tab, number][])" :key="x[0]" type="button" size="sm" role="tab"
        :aria-selected="tab === x[0]" :variant="tab === x[0] ? 'default' : 'outline'" :data-testid="`tab-${x[0]}`" @click="go(x[0])">{{ t(`inventory.purchasing.tab.${x[0]}`) }} · {{ x[1] }}</Button>
    </div>
    <div class="mb-3 flex flex-wrap items-center gap-2">
      <input v-model="q" type="search" name="q" class="h-9 w-full max-w-sm rounded-md border border-border bg-card px-3 text-sm" :placeholder="t('inventory.purchasing.search')" :aria-label="t('inventory.purchasing.search')" data-testid="purchasing-search" />
      <Button v-for="c in chips" :key="c.key" type="button" size="sm" class="rounded-full" :variant="chip === c.key ? 'default' : 'outline'" :data-testid="`chip-${c.key}`" @click="chip = c.key">{{ c.label }}</Button>
    </div>

    <DataTable v-if="tab === 'requests'" :columns="requestCols" :rows="shownRequests" row-key="id" :loading="!loaded" :empty-title="t('inventory.purchasing.emptyRequests')"
      :empty-description="t('emptyState.inventoryPurchasing')" :row-to="(r) => `/inventory/requests/${r.id}`" :row-test-id="(r) => `request-${r.request_number}`" data-testid="requests-table">
      <template #cell-request_number="{ row }"><RouterLink :to="`/inventory/requests/${row.id}`" class="text-primary hover:underline">{{ row.request_number }}</RouterLink></template>
      <template #cell-department_name="{ row }">{{ row.department_name ?? '—' }}</template>
      <template #cell-stage="{ row }">{{ stageText(row.stage, row.status) }}</template>
      <template #cell-status="{ row }"><Badge :variant="requestVariant(row.status)">{{ t(`inventory.requestStatus.${row.status}`) }}</Badge> <Badge v-if="row.waiting_for_me" variant="warning">{{ t('inventory.purchasing.waitingForMe') }}</Badge></template>
    </DataTable>
    <DataTable v-else-if="tab === 'orders'" :columns="orderCols" :rows="shownOrders" row-key="id" :loading="!loaded" :empty-title="t('inventory.purchasing.emptyOrders')"
      :empty-description="t('emptyState.inventoryPurchasing')" :row-to="(r) => `/inventory/purchase-orders/${r.id}`" :row-test-id="(r) => `order-${r.po_number}`" data-testid="orders-table">
      <template #cell-po_number="{ row }"><RouterLink :to="`/inventory/purchase-orders/${row.id}`" class="text-primary hover:underline">{{ row.po_number }}</RouterLink></template>
      <template #cell-request_number="{ row }">{{ row.request_number ?? '—' }}</template>
      <template #cell-received="{ row }">{{ receivedText(row) }}</template>
      <template #cell-status="{ row }"><Badge :variant="orderVariant(row.status)">{{ t(`inventory.orderStatus.${row.status}`) }}</Badge></template>
    </DataTable>
    <DataTable v-else-if="tab === 'receipts'" :columns="receiptCols" :rows="shownReceipts" row-key="id" :loading="!loaded" :empty-title="t('inventory.purchasing.emptyReceipts')"
      :empty-description="t('emptyState.inventoryPurchasing')" :row-to="(r) => `/inventory/receipts/${r.id}`" :row-test-id="(r) => `receipt-${r.receipt_number}`" data-testid="receipts-table">
      <template #cell-receipt_number="{ row }"><RouterLink :to="`/inventory/receipts/${row.id}`" class="text-primary hover:underline">{{ row.receipt_number }}</RouterLink></template>
      <template #cell-po_number="{ row }">{{ row.po_number ?? t('inventory.purchasing.noOrder') }}</template>
      <template #cell-draft="{ row }"><Badge v-if="row.draft_status" :variant="draftVariant(row.draft_status)">{{ t(`inventory.draftStatus.${row.draft_status}`) }}</Badge></template>
      <template #cell-status="{ row }"><Badge :variant="row.status === 'POSTED' ? 'success' : 'outline'">{{ t(`inventory.receiptStatus.${row.status}`) }}</Badge></template>
    </DataTable>
    <DataTable v-else :columns="returnCols" :rows="returns" row-key="id" :loading="!loaded" :empty-title="t('inventory.purchasing.emptyReturns')" :empty-description="t('emptyState.inventoryPurchasing')"
      :row-to="(r) => `/inventory/returns/${r.id}`" :row-test-id="(r) => `return-${r.return_number}`" data-testid="returns-table">
      <template #cell-return_number="{ row }"><RouterLink :to="`/inventory/returns/${row.id}`" class="text-primary hover:underline">{{ row.return_number }}</RouterLink></template>
      <template #cell-credit="{ row }">{{ row.credit_number ?? '—' }}</template>
    </DataTable>
    <p v-if="loaded && tab === 'requests' && requests.length > 0" class="muted mt-3 text-xs">{{ t('inventory.purchasing.updated', { date: formatDate(property.clock?.business_date) }) }}</p>
  </template>
</template>
