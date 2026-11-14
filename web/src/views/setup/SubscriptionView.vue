<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { SubscriptionInvoice } from '@/api/types'
import AccessBadge from '@/components/app/AccessBadge.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuotaMeter from '@/components/app/QuotaMeter.vue'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { t } from '@/i18n'
import { useSubscriptionStore } from '@/stores/subscription'
import { formatDate, formatMoney } from '@/utils/format'

/**
 * Setup → Subscription (administrators, read only; docs/architecture/39-platform-console.md §10.1): the plan and where its paid period stands, how much of each limit is used, the modules
 * and the invoices. Renewing and changing the plan is the operator's work, so there is no button here, only who to ask.
 */
const subscription = useSubscriptionStore()
const invoices = ref<SubscriptionInvoice[] | null>(null)
const invoiceError = ref<ApiError | null>(null)

onMounted(() => {
  void subscription.load(true)
  void (async () => {
    try {
      const { data } = await api.GET('/api/v1/subscription/invoices')
      invoices.value = data?.items ?? []
    } catch (e) {
      invoiceError.value = e instanceof ApiError ? e : null
      invoices.value = []
    }
  })()
})

const s = computed(() => subscription.summary)
const OPTIONAL = ['HOUSEKEEPING', 'ACCOUNTING', 'INVENTORY', 'BANQUET', 'POS'] as const
const meters = computed(() =>
  s.value
    ? ([
        { what: 'properties', used: s.value.usage.properties, limit: s.value.limits.max_properties },
        { what: 'rooms', used: s.value.usage.rooms, limit: s.value.limits.max_rooms },
        { what: 'users', used: s.value.usage.users, limit: s.value.limits.max_users },
      ] as const)
    : [],
)

// "Unpaid" is an issued invoice past its due day: the server says so (`is_overdue`, in the tenant's time zone), the browser does not count days.
const statusOf = (inv: SubscriptionInvoice): 'unpaid' | 'issued' | 'paid' | 'void' =>
  inv.status === 'PAID' ? 'paid' : inv.status === 'VOID' ? 'void' : inv.is_overdue ? 'unpaid' : 'issued'

const columns = computed<Column<SubscriptionInvoice>[]>(() => [
  { key: 'number', label: t('subscription.invoice.number') },
  { key: 'period', label: t('subscription.invoice.period') },
  { key: 'amount', label: t('subscription.invoice.amount'), align: 'right' },
  { key: 'status', label: t('subscription.invoice.status') },
  { key: 'due_on', label: t('subscription.invoice.due') },
])
</script>

<template>
  <PageHeader :title="t('subscription.title')" :description="t('subscription.description')" />

  <div v-if="!s && !subscription.error" class="grid gap-4" data-testid="subscription-loading">
    <Skeleton class="h-32 w-full" />
    <Skeleton class="h-40 w-full" />
  </div>
  <ErrorNotice v-else-if="!s" :error="subscription.error" inline />

  <div v-else class="grid gap-4">
    <Card aria-labelledby="sub-plan-title" data-testid="subscription-plan">
      <CardHeader class="flex-row items-center justify-between gap-3">
        <CardTitle id="sub-plan-title">{{ t('subscription.plan', { name: s.plan_name }) }}</CardTitle>
        <AccessBadge :access="s.access" :status="s.status" />
      </CardHeader>
      <CardContent>
        <dl class="m-0 grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
          <div>
            <dt class="text-muted-foreground">{{ t('subscription.paidThrough') }}</dt>
            <dd class="m-0 font-medium" data-testid="paid-through">{{ s.paid_through ? formatDate(s.paid_through) : t('subscription.noEnd') }}</dd>
          </div>
          <div v-if="s.grace_ends_on">
            <dt class="text-muted-foreground">{{ t('subscription.graceUntil') }}</dt>
            <dd class="m-0 font-medium" data-testid="grace-until">{{ formatDate(s.grace_ends_on) }}</dd>
          </div>
          <div v-if="s.days_left !== null">
            <dt class="text-muted-foreground">{{ s.access === 'GRACE' ? t('subscription.graceDaysLeft') : t('subscription.daysLeft') }}</dt>
            <dd class="m-0 font-medium" data-testid="days-left">{{ s.days_left }}</dd>
          </div>
          <div>
            <dt class="text-muted-foreground">{{ t('subscription.since') }}</dt>
            <dd class="m-0 font-medium">{{ formatDate(s.started_on) }}</dd>
          </div>
        </dl>
        <p v-if="subscription.billingContact" class="m-0 mt-4 text-sm" data-testid="billing-contact">{{ t('subscription.billingContact', { contact: subscription.billingContact }) }}</p>
      </CardContent>
    </Card>

    <Card aria-labelledby="sub-usage-title" data-testid="subscription-usage">
      <CardHeader><CardTitle id="sub-usage-title">{{ t('subscription.usage') }}</CardTitle></CardHeader>
      <CardContent class="grid gap-4 sm:grid-cols-3">
        <QuotaMeter v-for="m in meters" :key="m.what" :what="m.what" :label="t(`subscription.resource.${m.what}` as never)" :used="m.used" :limit="m.limit" />
      </CardContent>
    </Card>

    <Card aria-labelledby="sub-modules-title" data-testid="subscription-modules">
      <CardHeader><CardTitle id="sub-modules-title">{{ t('subscription.modules') }}</CardTitle></CardHeader>
      <CardContent>
        <ul class="m-0 grid list-none gap-2 p-0 sm:grid-cols-2 lg:grid-cols-3">
          <li class="flex items-center justify-between gap-2 rounded-md border border-border px-3 py-2 text-sm" data-testid="module-CORE">
            <span class="font-medium">{{ t('subscription.module.CORE') }}</span>
            <Badge variant="success">{{ t('subscription.always') }}</Badge>
          </li>
          <li v-for="m in OPTIONAL" :key="m" class="flex items-center justify-between gap-2 rounded-md border border-border px-3 py-2 text-sm" :data-testid="`module-${m}`">
            <span class="font-medium">{{ t(`subscription.module.${m}` as never) }}</span>
            <Badge :variant="s.modules.includes(m) ? 'success' : 'outline'">{{ s.modules.includes(m) ? t('subscription.included') : t('subscription.notIncluded') }}</Badge>
          </li>
        </ul>
      </CardContent>
    </Card>

    <Card aria-labelledby="sub-invoices-title" data-testid="subscription-invoices">
      <CardHeader><CardTitle id="sub-invoices-title">{{ t('subscription.invoices') }}</CardTitle></CardHeader>
      <CardContent>
        <ErrorNotice v-if="invoiceError" :error="invoiceError" inline />
        <DataTable v-else :columns="columns" :rows="invoices ?? []" row-key="id" :loading="invoices === null" :caption="t('subscription.invoices')" :empty-title="t('subscription.noInvoices')">
          <template #cell-period="{ row }">{{ formatDate(row.period_start) }} – {{ formatDate(row.period_end) }}</template>
          <template #cell-amount="{ row }">{{ row.currency_code }} {{ formatMoney(row.amount) }}</template>
          <template #cell-status="{ row }">
            <Badge :variant="statusOf(row) === 'unpaid' ? 'destructive' : statusOf(row) === 'paid' ? 'success' : 'outline'">{{ t(`subscription.invoice.${statusOf(row)}` as never) }}</Badge>
          </template>
          <template #cell-due_on="{ row }">{{ formatDate(row.due_on) }}</template>
        </DataTable>
      </CardContent>
    </Card>
  </div>
</template>
