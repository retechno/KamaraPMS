<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryBillDraftBrief } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { draftVariant } from '@/utils/purchasing'

/**
 * The draft bills the receipts made (docs/architecture/35-inventory.md 5.3.4, mockup 21): what has come in and has not been billed. A draft opens its own page to be matched against the supplier's invoice;
 * the drafts of one supplier can be billed together by an invoice that covers several deliveries.
 */
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canBill = computed(() => auth.can('inventory.bill', pid.value) && auth.can('payables.post', pid.value))

const drafts = ref<InventoryBillDraftBrief[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const open = ref(true)
const picked = ref<number[]>([])

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  picked.value = []
  if (propertyId === null) {
    loaded.value = true
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/bill-drafts', { params: { path: { propertyId }, query: open.value ? { open: true, limit: 200 } : { limit: 200 } } })
    drafts.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, open], load, { immediate: true })

const columns = computed<Column<InventoryBillDraftBrief>[]>(() => [
  { key: 'pick', label: '' },
  { key: 'receipt_number', label: t('inventory.drafts.receipt'), class: 'font-medium whitespace-nowrap' },
  { key: 'supplier_name', label: t('inventory.drafts.supplier') },
  { key: 'received_on', label: t('inventory.drafts.receivedOn'), format: 'date' },
  { key: 'po_number', label: t('inventory.drafts.order') },
  { key: 'warehouse_name', label: t('inventory.drafts.warehouse') },
  { key: 'receipt_value', label: t('inventory.drafts.receiptValue'), align: 'right', format: 'money' },
  { key: 'remaining_value', label: t('inventory.drafts.remaining'), align: 'right', format: 'money' },
  { key: 'status', label: t('inventory.drafts.status') },
])

const supplierOfPicked = computed(() => drafts.value.find((d) => picked.value.includes(d.id))?.supplier_id ?? null)
const canPick = (d: InventoryBillDraftBrief): boolean => canBill.value && (d.status === 'OPEN' || d.status === 'PARTIAL') && (supplierOfPicked.value === null || supplierOfPicked.value === d.supplier_id)
function toggle(d: InventoryBillDraftBrief): void {
  picked.value = picked.value.includes(d.id) ? picked.value.filter((x) => x !== d.id) : [...picked.value, d.id]
}
function billPicked(): void {
  const [first, ...rest] = picked.value
  if (first === undefined) return
  void router.push({ path: `/inventory/bill-drafts/${first}`, query: rest.length ? { with: rest.join(',') } : {} })
}
</script>

<template>
  <PageHeader :title="t('inventory.drafts.title')" :description="t('inventory.drafts.intro')">
    <template #actions>
      <Button type="button" :disabled="picked.length === 0" data-testid="bill-picked" @click="billPicked">{{ t('inventory.drafts.billPicked', { n: picked.length }) }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <div class="mb-3 flex flex-wrap items-center gap-2">
      <Button type="button" size="sm" class="rounded-full" :variant="open ? 'default' : 'outline'" data-testid="chip-open" @click="open = true">{{ t('inventory.drafts.chipOpen') }}</Button>
      <Button type="button" size="sm" class="rounded-full" :variant="open ? 'outline' : 'default'" data-testid="chip-all" @click="open = false">{{ t('inventory.drafts.chipAll') }}</Button>
    </div>
    <DataTable :columns="columns" :rows="drafts" row-key="id" :loading="!loaded" :empty-title="t('inventory.drafts.empty')" :empty-description="t('emptyState.inventoryDrafts')"
      :row-to="(r) => `/inventory/bill-drafts/${r.id}`" :row-test-id="(r) => `draft-${r.receipt_number}`" data-testid="drafts-table">
      <template #cell-pick="{ row }"><input type="checkbox" :checked="picked.includes(row.id)" :disabled="!canPick(row)" :aria-label="t('inventory.drafts.pick', { number: row.receipt_number })" :data-testid="`pick-${row.receipt_number}`" @change="toggle(row)" /></template>
      <template #cell-receipt_number="{ row }"><RouterLink :to="`/inventory/bill-drafts/${row.id}`" class="text-primary hover:underline">{{ row.receipt_number }}</RouterLink></template>
      <template #cell-po_number="{ row }">{{ row.po_number ?? t('inventory.drafts.noOrder') }}</template>
      <template #cell-status="{ row }"><Badge :variant="draftVariant(row.status)">{{ t(`inventory.draftStatus.${row.status}`) }}</Badge></template>
    </DataTable>
  </template>
</template>
