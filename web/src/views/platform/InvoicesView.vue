<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { platformApi } from '@/api/platformClient'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import RecordPaymentDialog from '@/components/platform/RecordPaymentDialog.vue'
import VoidInvoiceDialog from '@/components/platform/VoidInvoiceDialog.vue'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { todayIn } from '@/utils/dates'
import { formatDate } from '@/utils/format'
import { platformMoney } from '@/utils/platformMoney'

/**
 * P9, the invoices of every tenant (docs/architecture/39-platform-console.md 11): tabs Open / Overdue / Paid / Voided with their counts, and the actions of an issued invoice. The API has
 * no counts, so the four lists are read together and a count is the length of its list; after a payment or a void they are read again.
 */
type Invoice = components['schemas']['PlatformInvoiceRow']
type Total = components['schemas']['PlatformCurrencyTotal']
type Tab = 'open' | 'overdue' | 'paid' | 'void'
const TABS: Tab[] = ['open', 'overdue', 'paid', 'void']

const tab = ref<Tab>('open')
const lists = ref<Record<Tab, Invoice[]> | null>(null)
const totals = ref<Record<Tab, Total[]>>({ open: [], overdue: [], paid: [], void: [] })
const error = ref<ApiError | null>(null)
const paying = ref<Invoice | null>(null)
const payOpen = ref(false)
const voiding = ref<Invoice | null>(null)
const voidOpen = ref(false)

async function load(): Promise<void> {
  error.value = null
  try {
    const answers = await Promise.all(TABS.map((status) => platformApi.GET('/api/v1/platform/invoices', { params: { query: { status } } })))
    const next = { open: [], overdue: [], paid: [], void: [] } as Record<Tab, Invoice[]>
    const nextTotals = { open: [], overdue: [], paid: [], void: [] } as Record<Tab, Total[]>
    TABS.forEach((status, i) => {
      next[status] = answers[i]?.data?.items ?? []
      nextTotals[status] = answers[i]?.data?.total_open ?? []
    })
    lists.value = next
    totals.value = nextTotals
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    lists.value = lists.value ?? { open: [], overdue: [], paid: [], void: [] }
  }
}
onMounted(load)

const rows = computed(() => lists.value?.[tab.value] ?? [])
const loading = computed(() => lists.value === null)
const countOf = (status: Tab): number => lists.value?.[status].length ?? 0

// "in N days" is a hint for the operator: the server says only how late an invoice is (in the tenant's time zone), so the days to come are counted from the operator's own day.
const operatorToday = (): string => todayIn(Intl.DateTimeFormat().resolvedOptions().timeZone) ?? ''
function daysUntil(date: string): number {
  const today = operatorToday()
  if (!today) return 0
  return Math.round((Date.parse(`${date}T00:00:00Z`) - Date.parse(`${today}T00:00:00Z`)) / 86_400_000)
}
function dueHint(inv: Invoice): { text: string; late: boolean } | null {
  if (inv.status !== 'ISSUED') return null
  if (inv.overdue_days > 0) return { text: inv.overdue_days === 1 ? t('platform.invoices.lateOne') : t('platform.invoices.lateDays', { n: inv.overdue_days }), late: true }
  const n = daysUntil(inv.due_on)
  return { text: n > 0 ? t('platform.invoices.dueIn', { n }) : t('platform.invoices.dueToday'), late: false }
}

const columns = computed<Column<Invoice>[]>(() => [
  { key: 'number', label: t('platform.invoices.col.number') },
  { key: 'tenant', label: t('platform.invoices.col.tenant') },
  { key: 'plan_code', label: t('platform.invoices.col.plan') },
  { key: 'period', label: t('platform.invoices.col.period') },
  { key: 'amount', label: t('platform.invoices.col.amount'), align: 'right' },
  { key: 'due_on', label: t('platform.invoices.col.due') },
  { key: 'ops', label: t('platform.invoices.col.actions') },
])

function openPay(inv: Invoice): void {
  paying.value = inv
  payOpen.value = true
}
function openVoid(inv: Invoice): void {
  voiding.value = inv
  voidOpen.value = true
}
function onPaid(): void {
  if (paying.value) toast.success(t('platform.invoices.paidDone', { number: paying.value.number }))
  void load()
}
function onVoided(): void {
  if (voiding.value) toast.success(t('platform.invoices.voidedDone', { number: voiding.value.number }))
  void load()
}
// `total_open` is what is owed in all (every issued invoice), so it is the footer of the Open tab only
const showTotal = computed(() => tab.value === 'open')
</script>

<template>
  <PageHeader :title="t('platform.nav.invoices')" :description="t('platform.invoices.description')" />

  <Tabs :model-value="tab" @update:model-value="tab = String($event) as Tab">
    <TabsList :aria-label="t('platform.invoices.tabsLabel')" data-testid="invoice-tabs">
      <TabsTrigger v-for="s in TABS" :key="s" :value="s" :data-testid="`tab-${s}`">
        {{ t(`platform.invoices.tab.${s}` as never) }}
        <span class="rounded-full bg-muted px-1.5 text-xs tabular-nums text-muted-foreground" :data-testid="`count-${s}`">{{ countOf(s) }}</span>
      </TabsTrigger>
    </TabsList>
  </Tabs>

  <ErrorNotice :error="error" inline class="mt-3" data-testid="invoices-error" />

  <section class="mt-3" data-testid="invoice-list">
    <DataTable
      :columns="columns"
      :rows="rows"
      row-key="number"
      :loading="loading"
      :caption="t(`platform.invoices.caption.${tab}` as never)"
      :empty-title="t(`platform.invoices.empty.${tab}` as never)"
      :row-test-id="(r) => `invoice-${r.number}`"
    >
      <template #cell-number="{ row }"><span class="whitespace-nowrap font-medium">{{ row.number }}</span></template>
      <template #cell-tenant="{ row }">
        <RouterLink :to="`/platform/tenants/${encodeURIComponent(row.tenant_code)}`" class="font-medium text-primary hover:underline" data-testid="tenant-link">{{ row.tenant_name }}</RouterLink>
        <span class="ml-1 text-xs text-muted-foreground">{{ row.tenant_code }}</span>
      </template>
      <template #cell-period="{ row }"><span class="whitespace-nowrap">{{ formatDate(row.period_start) }} – {{ formatDate(row.period_end) }}</span></template>
      <template #cell-amount="{ row }"><span class="whitespace-nowrap tabular-nums">{{ platformMoney(row.currency_code, row.amount) }}</span></template>
      <template #cell-due_on="{ row }">
        <div class="flex flex-col">
          <span>{{ formatDate(row.due_on) }}</span>
          <span v-if="dueHint(row)" :class="['text-xs', dueHint(row)?.late ? 'font-medium text-destructive' : 'text-muted-foreground']" data-testid="due-hint">{{ dueHint(row)?.text }}</span>
          <span v-else-if="row.status === 'PAID' && row.paid_on" class="text-xs text-muted-foreground">{{ t('platform.invoices.paidOn', { date: formatDate(row.paid_on) }) }}{{ row.payment_reference ? ` · ${row.payment_reference}` : '' }}</span>
          <span v-else-if="row.status === 'VOID' && row.void_reason" class="text-xs text-muted-foreground">{{ t('platform.invoices.voidedWith', { reason: row.void_reason }) }}</span>
        </div>
      </template>
      <template #cell-ops="{ row }">
        <div v-if="row.status === 'ISSUED'" class="flex flex-wrap gap-2">
          <Button size="sm" variant="outline" :aria-label="t('platform.invoices.recordPaymentFor', { number: row.number })" data-testid="pay-invoice" @click="openPay(row)">{{ t('platform.invoices.recordPayment') }}</Button>
          <Button size="sm" variant="outline" :aria-label="t('platform.invoices.voidFor', { number: row.number })" data-testid="void-invoice" @click="openVoid(row)">{{ t('platform.invoices.voidAction') }}</Button>
        </div>
      </template>
      <template #footer>
        <p v-if="!loading" class="m-0 mt-2 text-sm text-muted-foreground" data-testid="invoice-count">{{ t(`platform.invoices.count.${tab}` as never, { n: rows.length }) }}</p>
        <p v-if="showTotal && totals[tab].length" class="m-0 mt-2 flex flex-wrap justify-end gap-x-3 text-sm" data-testid="invoice-total">
          <span class="text-muted-foreground">{{ t('platform.invoices.totalOpen') }}</span>
          <strong v-for="c in totals[tab]" :key="c.currency_code" class="tabular-nums" :data-testid="`total-${c.currency_code}`">{{ platformMoney(c.currency_code, c.total) }}</strong>
        </p>
      </template>
    </DataTable>
  </section>

  <RecordPaymentDialog v-if="paying" v-model:open="payOpen" :invoice="paying" @paid="onPaid" />
  <VoidInvoiceDialog v-if="voiding" v-model:open="voidOpen" :invoice="voiding" @voided="onVoided" />
</template>
