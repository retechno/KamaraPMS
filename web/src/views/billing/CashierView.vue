<script setup lang="ts">
import FilterBar from '@/components/app/FilterBar.vue'
import type { RowAction } from '@/components/app/rowActions'
import { usePagedList } from '@/composables/usePagedList'
import { FileText, Receipt, Search } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { MethodTotal, Payment } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, openPdf } from '@/utils/documents'

const auth = useAuthStore()
const property = usePropertyStore()

const date = ref('')
const method = ref('')
const totals = ref<MethodTotal[]>([])

const METHODS = ['CASH', 'CARD', 'BANK_TRANSFER', 'OTHER'] as const

const canRead = computed(() => auth.can('folio.read', property.currentId))
const canPrint = computed(() => auth.can('reservation.read', property.currentId)) // a receipt names the guest and the reservation
const businessDate = computed(() => property.clock?.business_date ?? '')

const methodLabel = (m: string): string => t(`cashier.${m}` as never)

// The business date of the day is what the page shows to begin with: another day, or a method, is a filter that is on.
const activeFilters = computed(() => (date.value && date.value !== businessDate.value ? 1 : 0) + (method.value ? 1 : 0))

const columns = computed<Column<Payment>[]>(() => [
  { key: 'payment_number', label: t('cashier.number'), sortable: true, card: 'primary' as const },
  { key: 'payment_type', label: t('cashier.type'), sortable: true },
  { key: 'payment_method', label: t('cashier.method'), sortable: true, card: 'secondary' as const },
  { key: 'amount', label: t('cashier.amount'), align: 'right', sortable: true, class: 'tabular-nums', format: 'money' as const, card: 'money' as const },
  { key: 'status', label: t('cashier.status'), sortable: true, card: 'badge' as const },
  { key: 'folio_id', label: t('cashier.folio') },
  ...(canPrint.value ? [{ key: 'actions', label: '', align: 'right' as const }] : []),
])

/** The receipt is the main action of a payment: a button in the table and in the card. */
const actionsOf = (p: Payment): RowAction[] => [{ key: 'receipt', label: t('cashier.receipt'), primary: true, testId: `receipt-${p.payment_number}`, onSelect: () => void print(p.id) }]

async function print(paymentId: number): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  error.value = null
  try {
    await openPdf(documentPath.receipt(propertyId, paymentId))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

// One page of 50 at a time; the day and the method are asked of the server, and a new search starts from the first page (the totals of the day come with it).
// The totals come with the first page of the day. A search that was replaced by a later one (the day arrived while the first was on its way) must not write its totals over the new ones.
let latestTotals = 0
let askedWithoutDay = false
const list = usePagedList<Payment>(async (cursor) => {
  const mine = cursor ? latestTotals : ++latestTotals
  const { data } = await api.GET('/api/v1/properties/{propertyId}/payments', {
    params: {
      path: { propertyId: property.currentId! },
      query: { limit: 50, cursor, business_date: date.value || undefined, method: (method.value || undefined) as 'CASH' | undefined },
    },
  })
  if (!cursor && mine === latestTotals) totals.value = data?.totals ?? []
  return { data: data?.data ?? [], next_cursor: data?.next_cursor }
})
const { rows, error, loading, loadingMore, loaded: searched, hasMore } = list

function clearFilters(): void {
  date.value = businessDate.value
  method.value = ''
  void load()
}

async function load(): Promise<void> {
  if (property.currentId === null || !canRead.value) return
  if (!date.value) date.value = businessDate.value
  askedWithoutDay = !date.value // the clock of the property is not in yet: ask again when it is
  await list.reload()
}

watch(() => property.currentId, () => {
  date.value = ''
  list.reset()
  void load()
}, { immediate: true })
watch(businessDate, () => {
  if (!searched.value || askedWithoutDay) void load()
})
</script>

<template>
  <PageHeader :title="t('cashier.title')">
    <template #actions>
      <Button as-child variant="outline" size="sm">
        <RouterLink to="/folios"><FileText />{{ t('cashier.foliosLink') }}</RouterLink>
      </Button>
    </template>
  </PageHeader>

  <ErrorNotice :error="error" :inline="!searched" />
  <p v-if="property.currentId === null" class="muted">{{ t('cashier.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('cashier.noAccess') }}</p>

  <template v-else>
    <FilterBar class="mb-4 flex flex-wrap items-end gap-x-3 gap-y-6 pb-5" role="search" :active="activeFilters" @submit="load">

      <FormField float-hint :hint="$weekday(date)" class="w-44" :label="t('cashier.businessDate')">
        <template #default="{ id }"><Input :id="id" v-model="date" name="business_date" type="date" /></template>
      </FormField>
      <FormField class="w-44" :label="t('cashier.method')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="method" name="method">
            <option value="">{{ t('cashier.any') }}</option>
            <option v-for="m in METHODS" :key="m" :value="m">{{ methodLabel(m) }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <template #actions>
        <Button type="submit" :disabled="loading"><Search />{{ t('cashier.show') }}</Button>
      </template>
    </FilterBar>

    <div v-if="totals.length" class="mb-5 grid grid-cols-[repeat(auto-fill,minmax(12rem,1fr))] gap-3" data-testid="totals">
      <KpiCard
        v-for="tot in totals"
        :key="tot.payment_method"
        :data-testid="`total-${tot.payment_method}`"
        :label="methodLabel(tot.payment_method)"
        :value="$money(tot.net)"
        :hint="t('cashier.paidRefunded', { paid: $money(tot.paid), refunded: $money(tot.refunded) })"
        :icon="Receipt"
      />
    </div>

    <DataTable
      :columns="columns"
      :rows="rows"
      row-key="id"
      :loading="!searched"
      :has-more="hasMore"
      :loading-more="loadingMore"
      cards
      :row-actions="actionsOf"
      :row-test-id="(p) => `payment-${p.payment_number}`"
      :row-class="(p) => (p.status === 'VOIDED' ? 'struck text-muted-foreground line-through' : undefined)"
      :caption="t('cashier.title')"
      @load-more="list.loadMore()"
    >
      <template #cell-payment_type="{ row }">
        <Badge :variant="row.payment_type === 'REFUND' ? 'warning' : 'outline'">{{ t(`cashier.${row.payment_type}` as never) }}</Badge>
      </template>
      <template #cell-payment_method="{ row }">{{ methodLabel(row.payment_method) }}</template>
      <template #cell-status="{ row }"><StatusBadge domain="payment" :status="row.status" /></template>
      <template #cell-folio_id="{ row }"><RouterLink :to="`/folios/${row.folio_id}`">{{ row.folio_number || `#${row.folio_id}` }}</RouterLink></template>
      <template #empty><EmptyState :description="t('emptyState.cashier')" :action-label="activeFilters ? t('dataTable.clearFilters') : ''" action-variant="outline" @action="clearFilters" :title="t('cashier.empty')" data-testid="empty" /></template>
    </DataTable>
  </template>
</template>
