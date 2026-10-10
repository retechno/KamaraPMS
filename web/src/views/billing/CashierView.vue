<script setup lang="ts">
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
const rows = ref<Payment[]>([])
const totals = ref<MethodTotal[]>([])
const nextCursor = ref<string | undefined>()
const error = ref<ApiError | null>(null)
const loading = ref(false)
const searched = ref(false)

const METHODS = ['CASH', 'CARD', 'BANK_TRANSFER', 'OTHER'] as const

const canRead = computed(() => auth.can('folio.read', property.currentId))
const canPrint = computed(() => auth.can('reservation.read', property.currentId)) // a receipt names the guest and the reservation
const businessDate = computed(() => property.clock?.business_date ?? '')

const methodLabel = (m: string): string => t(`cashier.${m}` as never)

const columns = computed<Column<Payment>[]>(() => [
  { key: 'payment_number', label: t('cashier.number'), sortable: true },
  { key: 'payment_type', label: t('cashier.type'), sortable: true },
  { key: 'payment_method', label: t('cashier.method'), sortable: true },
  { key: 'amount', label: t('cashier.amount'), align: 'right', sortable: true, class: 'tabular-nums', format: 'money' as const },
  { key: 'status', label: t('cashier.status'), sortable: true },
  { key: 'folio_id', label: t('cashier.folio') },
  ...(canPrint.value ? [{ key: 'receipt', label: '', align: 'right' as const }] : []),
])

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

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  if (!date.value) date.value = businessDate.value
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payments', {
      params: {
        path: { propertyId },
        query: { limit: 50, cursor: more ? nextCursor.value : undefined, business_date: date.value || undefined, method: (method.value || undefined) as 'CASH' | undefined },
      },
    })
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    if (!more) totals.value = data?.totals ?? []
    nextCursor.value = data?.next_cursor
    searched.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

watch(() => property.currentId, () => {
  date.value = ''
  rows.value = []
  searched.value = false
  void load()
}, { immediate: true })
watch(businessDate, () => {
  if (!searched.value) void load()
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
    <form class="mb-4 flex flex-wrap items-end gap-3 pb-5" role="search" @submit.prevent="load()">
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
      <Button type="submit" :disabled="loading"><Search />{{ t('cashier.show') }}</Button>
    </form>

    <div v-if="totals.length" class="mb-5 grid grid-cols-[repeat(auto-fill,minmax(12rem,1fr))] gap-3" data-testid="totals">
      <KpiCard
        v-for="tot in totals"
        :key="tot.payment_method"
        :data-testid="`total-${tot.payment_method}`"
        :label="methodLabel(tot.payment_method)"
        :value="tot.net"
        :hint="t('cashier.paidRefunded', { paid: tot.paid, refunded: tot.refunded })"
        :icon="Receipt"
      />
    </div>

    <DataTable
      :columns="columns"
      :rows="rows"
      row-key="id"
      :loading="!searched"
      :row-test-id="(p) => `payment-${p.payment_number}`"
      :row-class="(p) => (p.status === 'VOIDED' ? 'struck text-muted-foreground line-through' : undefined)"
      :caption="t('cashier.title')"
    >
      <template #cell-payment_type="{ row }">
        <Badge :variant="row.payment_type === 'REFUND' ? 'warning' : 'outline'">{{ t(`cashier.${row.payment_type}` as never) }}</Badge>
      </template>
      <template #cell-payment_method="{ row }">{{ methodLabel(row.payment_method) }}</template>
      <template #cell-status="{ row }"><StatusBadge domain="payment" :status="row.status" /></template>
      <template #cell-folio_id="{ row }"><RouterLink :to="`/folios/${row.folio_id}`">#{{ row.folio_id }}</RouterLink></template>
      <template #cell-receipt="{ row }">
        <Button variant="outline" size="sm" :data-testid="`receipt-${row.payment_number}`" @click="print(row.id)">{{ t('cashier.receipt') }}</Button>
      </template>
      <template #empty><EmptyState :title="t('cashier.empty')" data-testid="empty" /></template>
      <template #footer>
        <div v-if="nextCursor" class="flex justify-center p-3">
          <Button variant="outline" size="sm" :disabled="loading" data-testid="more" @click="load(true)">{{ t('cashier.loadMore') }}</Button>
        </div>
      </template>
    </DataTable>
  </template>
</template>
