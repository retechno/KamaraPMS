<script setup lang="ts">
import { TriangleAlert } from 'lucide-vue-next'
import { computed, onMounted, ref, watch } from 'vue'
import { platformApi } from '@/api/platformClient'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import AccessBadge from '@/components/app/AccessBadge.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuotaMeter from '@/components/app/QuotaMeter.vue'
import ChangeSubscriptionDialog from '@/components/platform/ChangeSubscriptionDialog.vue'
import IssueInvoiceDialog from '@/components/platform/IssueInvoiceDialog.vue'
import RecordPaymentDialog from '@/components/platform/RecordPaymentDialog.vue'
import SuspendTenantDialog from '@/components/platform/SuspendTenantDialog.vue'
import VoidInvoiceDialog from '@/components/platform/VoidInvoiceDialog.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { t } from '@/i18n'
import { formatDate, formatDateTime } from '@/utils/format'
import { platformMoney } from '@/utils/platformMoney'

type Tenant = components['schemas']['PlatformTenantDetail']
type Plan = components['schemas']['PlatformPlan']
type Admin = components['schemas']['PlatformTenantAdmin']
type Activity = components['schemas']['PlatformActivityEntry']
type Invoice = components['schemas']['PlatformInvoiceRow']
type Resource = 'properties' | 'rooms' | 'users'

/**
 * P5, one tenant for the operator (docs/architecture/39-platform-console.md 11): where its subscription stands, the quotas and what is used, the modules, the invoices, the administrators
 * (contacts only) and the latest changes. The actions are the dialogs of `components/platform`; after each one the page reads everything again.
 */
const props = defineProps<{ code: string }>()

const tenant = ref<Tenant | null>(null)
const loadError = ref<ApiError | null>(null)
const plans = ref<Plan[] | null>(null)
const invoices = ref<Invoice[] | null>(null)
const invoiceError = ref<ApiError | null>(null)
const admins = ref<Admin[] | null>(null)
const adminError = ref<ApiError | null>(null)
const activity = ref<Activity[] | null>(null)
const activityError = ref<ApiError | null>(null)
const activityLimit = ref(5)
let ticket = 0

const FEW = 5
const ALL = 200

const asError = (e: unknown): ApiError => {
  if (e instanceof ApiError) return e
  throw e
}

async function loadActivity(mine: number): Promise<void> {
  try {
    const { data } = await platformApi.GET('/api/v1/platform/tenants/{code}/activity', { params: { path: { code: props.code }, query: { limit: activityLimit.value } } })
    if (mine === ticket) {
      activity.value = data?.items ?? []
      activityError.value = null
    }
  } catch (e) {
    if (mine === ticket) activityError.value = asError(e)
  }
}

async function load(): Promise<void> {
  const mine = ++ticket
  const path = { code: props.code }
  const parts = [
    (async () => {
      try {
        const { data } = await platformApi.GET('/api/v1/platform/tenants/{code}', { params: { path } })
        if (mine === ticket && data) {
          tenant.value = data
          loadError.value = null
        }
      } catch (e) {
        if (mine === ticket) {
          loadError.value = asError(e)
          tenant.value = null
        }
      }
    })(),
    (async () => {
      try {
        const { data } = await platformApi.GET('/api/v1/platform/tenants/{code}/invoices', { params: { path } })
        if (mine === ticket) {
          invoices.value = (data?.items ?? []).map((i) => ({ ...i, tenant_code: props.code, tenant_name: tenant.value?.name ?? props.code, overdue_days: 0 }))
          invoiceError.value = null
        }
      } catch (e) {
        if (mine === ticket) {
          invoiceError.value = asError(e)
          invoices.value = []
        }
      }
    })(),
    (async () => {
      try {
        const { data } = await platformApi.GET('/api/v1/platform/tenants/{code}/admins', { params: { path } })
        if (mine === ticket) {
          admins.value = data?.items ?? []
          adminError.value = null
        }
      } catch (e) {
        if (mine === ticket) {
          adminError.value = asError(e)
          admins.value = []
        }
      }
    })(),
    loadActivity(mine),
    (async () => {
      try {
        const { data } = await platformApi.GET('/api/v1/platform/plans')
        if (mine === ticket) plans.value = data?.items ?? []
      } catch {
        // without the plans the page shows no price and no plan limit: the rest of it still works
        if (mine === ticket) plans.value = plans.value ?? []
      }
    })(),
  ]
  await Promise.all(parts)
}

onMounted(load)
watch(() => props.code, () => {
  tenant.value = null
  invoices.value = null
  admins.value = null
  activity.value = null
  activityLimit.value = FEW
  void load()
})

async function seeAll(): Promise<void> {
  activityLimit.value = ALL
  await loadActivity(ticket)
}

const planOf = computed(() => plans.value?.find((p) => p.code === tenant.value?.summary.plan_code) ?? null)
const suspended = computed(() => tenant.value?.status === 'SUSPENDED')
const summary = computed(() => tenant.value?.summary ?? null)
const money = platformMoney

const meta = computed(() => {
  const x = tenant.value
  if (!x) return ''
  return t('platform.tenant.meta', { code: x.code, zone: x.timezone, since: formatDate(x.created_at.slice(0, 10)), properties: t('platform.tenant.properties', { n: x.property_count }, x.property_count) })
})

const openInvoice = computed(() => invoices.value?.find((i) => i.status === 'ISSUED') ?? null)

const callout = computed<{ kind: 'warning' | 'danger'; text: string } | null>(() => {
  const x = tenant.value
  const s = summary.value
  if (!x || !s) return null
  if (x.status === 'SUSPENDED') return { kind: 'danger', text: t('platform.tenant.callout.suspended', { reason: x.status_reason ?? '' }) }
  const paid = s.paid_through ? formatDate(s.paid_through) : ''
  const grace = s.grace_ends_on ? formatDate(s.grace_ends_on) : ''
  if (s.access === 'READ_ONLY') return { kind: 'danger', text: t('platform.tenant.callout.readOnly', { paid, grace }) }
  if (s.access === 'GRACE') return { kind: 'warning', text: t('platform.tenant.callout.grace', { paid, grace, n: s.days_left ?? 0 }) }
  if (s.is_trial_ending) return { kind: 'warning', text: t('platform.tenant.callout.trialEnding', { date: paid, n: s.days_left ?? 0 }) }
  return null
})

const RESOURCES: Resource[] = ['properties', 'rooms', 'users']
const LIMIT = { properties: 'max_properties', rooms: 'max_rooms', users: 'max_users' } as const
const meters = computed(() => {
  const x = tenant.value
  if (!x) return []
  return RESOURCES.map((r) => {
    const own = x.subscription.overrides[LIMIT[r]]
    const planLimit = planOf.value ? (planOf.value.limits[LIMIT[r]] ?? null) : undefined
    return {
      what: r,
      used: x.summary.usage[r],
      limit: x.summary.limits[LIMIT[r]],
      override: own !== null,
      planText: planLimit === undefined ? '–' : planLimit === null ? t('platform.tenant.quotas.planUnlimited') : String(planLimit),
    }
  })
})

const OPTIONAL = ['HOUSEKEEPING', 'ACCOUNTING', 'INVENTORY', 'BANQUET', 'POS'] as const

const facts = computed(() => {
  const x = tenant.value
  const s = summary.value
  if (!x || !s) return []
  const p = planOf.value
  return [
    { key: 'plan', label: t('platform.tenant.subscription.plan'), value: `${s.plan_code} · ${s.plan_name}` },
    { key: 'status', label: t('platform.tenant.subscription.status'), value: t(`platform.tenant.subscription.state.${x.subscription.status}` as never) },
    { key: 'started', label: t('platform.tenant.subscription.started'), value: formatDate(s.started_on) },
    { key: 'paid-through', label: t('platform.tenant.subscription.paidThrough'), value: s.paid_through ? formatDate(s.paid_through) : t('platform.tenant.subscription.noEnd') },
    { key: 'grace-days', label: t('platform.tenant.subscription.graceDays'), value: t('platform.tenant.subscription.graceValue', { n: x.subscription.grace_days }) },
    ...(p ? [{ key: 'billing', label: t('platform.tenant.subscription.billing'), value: t('platform.tenant.subscription.billingValue', { amount: money(p.currency_code, p.price), n: p.billing_period_months }) }] : []),
  ]
})

type InvoiceState = 'overdue' | 'issued' | 'paid' | 'void'
const stateOf = (i: Invoice): InvoiceState => (i.status === 'PAID' ? 'paid' : i.status === 'VOID' ? 'void' : i.is_overdue ? 'overdue' : 'issued')
const stateVariant = { overdue: 'destructive', issued: 'warning', paid: 'success', void: 'outline' } as const

const columns = computed<Column<Invoice>[]>(() => [
  { key: 'number', label: t('platform.tenant.invoices.number') },
  { key: 'period', label: t('platform.tenant.invoices.period') },
  { key: 'amount', label: t('platform.tenant.invoices.amount'), align: 'right' },
  { key: 'status', label: t('platform.tenant.invoices.status') },
  { key: 'due_on', label: t('platform.tenant.invoices.due') },
  { key: 'ops', label: t('platform.tenant.invoices.actions') },
])

const ACTIONS: Record<string, string> = {
  'subscription.started': 'subscriptionStarted',
  'subscription.changed': 'subscriptionChanged',
  'subscription.cancelled': 'subscriptionCancelled',
  'subscription.renewed': 'subscriptionRenewed',
  'subscription_invoice.issued': 'invoiceIssued',
  'subscription_invoice.paid': 'invoicePaid',
  'subscription_invoice.voided': 'invoiceVoided',
  'tenant.created': 'tenantCreated',
  'tenant.suspended': 'tenantSuspended',
  'tenant.reactivated': 'tenantReactivated',
}
const whatOf = (a: Activity): string => (ACTIONS[a.action] ? t(`platform.tenant.activity.what.${ACTIONS[a.action]}` as never) : t('platform.tenant.activity.unknown'))
const whoOf = (a: Activity): string => (a.actor_kind === 'system' || !a.actor_name ? t('platform.tenant.activity.system') : a.actor_name)

// dialogs
const changeOpen = ref(false)
const changeFocus = ref<'quotas' | undefined>(undefined)
const issueOpen = ref(false)
const suspendOpen = ref(false)
const reactivateOpen = ref(false)
const payFor = ref<Invoice | null>(null)
const voidFor = ref<Invoice | null>(null)

function openChange(focus?: 'quotas'): void {
  changeFocus.value = focus
  changeOpen.value = true
}
</script>

<template>
  <div>
    <nav :aria-label="t('platform.tenant.crumb.label')" class="mb-3 text-sm text-muted-foreground" data-testid="tenant-breadcrumb">
      <RouterLink :to="{ name: 'platform-tenants' }" class="text-primary underline-offset-4 hover:underline">{{ t('platform.tenant.crumb.tenants') }}</RouterLink>
      <span aria-hidden="true"> / </span>
      <span>{{ code }}</span>
    </nav>

    <PageHeader :title="tenant?.name ?? code" :description="meta || undefined">
      <template v-if="tenant" #marks>
        <Badge v-if="suspended" variant="destructive" data-testid="tenant-status">{{ t('platform.tenant.suspendedBadge') }}</Badge>
        <AccessBadge v-else-if="summary" :access="summary.access" :status="summary.status" />
      </template>
      <template v-if="tenant" #actions>
        <Button variant="outline" data-testid="action-change" @click="openChange()">{{ t('platform.tenant.actions.change') }}</Button>
        <Button variant="outline" data-testid="action-issue" @click="issueOpen = true">{{ t('platform.tenant.actions.issue') }}</Button>
        <Button v-if="suspended" variant="outline" data-testid="action-reactivate" @click="reactivateOpen = true">{{ t('platform.tenant.actions.reactivate') }}</Button>
        <Button v-else variant="outline" class="border-destructive text-destructive hover:bg-destructive/10" data-testid="action-suspend" @click="suspendOpen = true">{{ t('platform.tenant.actions.suspend') }}</Button>
      </template>
    </PageHeader>

    <div v-if="!tenant && !loadError" class="grid grid-cols-1 gap-4" data-testid="tenant-loading" role="status" :aria-label="t('platform.tenant.loading')">
      <Skeleton class="h-16 w-full" />
      <Skeleton class="h-40 w-full" />
    </div>

    <div v-else-if="!tenant" data-testid="tenant-error">
      <ErrorNotice :error="loadError" inline />
      <p class="mt-3">
        <RouterLink :to="{ name: 'platform-tenants' }" class="text-primary underline-offset-4 hover:underline" data-testid="tenant-back">{{ t('platform.tenant.back') }}</RouterLink>
      </p>
    </div>

    <template v-else>
      <div
        v-if="callout"
        :class="['mb-4 flex flex-wrap items-center gap-3 rounded-lg border p-3 text-sm', callout.kind === 'danger' ? 'border-destructive bg-destructive/10' : 'border-warning bg-warning/15']"
        role="status"
        data-testid="tenant-callout"
        :data-kind="callout.kind"
      >
        <TriangleAlert class="size-5 shrink-0" aria-hidden="true" />
        <span class="min-w-0 flex-1">
          {{ callout.text }}
          <template v-if="openInvoice"> {{ t('platform.tenant.callout.openInvoice', { number: openInvoice.number, amount: money(openInvoice.currency_code, openInvoice.amount) }) }}</template>
        </span>
        <Button v-if="openInvoice" size="sm" data-testid="callout-pay" @click="payFor = openInvoice">{{ t('platform.tenant.callout.recordPayment') }}</Button>
      </div>

      <div class="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div class="grid min-w-0 grid-cols-1 content-start gap-4">
          <Card aria-labelledby="tenant-sub-title" data-testid="tenant-subscription">
            <CardHeader class="flex-row items-center justify-between gap-3">
              <CardTitle id="tenant-sub-title">{{ t('platform.tenant.subscription.title') }}</CardTitle>
              <Button variant="ghost" size="sm" data-testid="subscription-change" @click="openChange()">{{ t('platform.tenant.subscription.change') }}</Button>
            </CardHeader>
            <CardContent>
              <dl class="m-0 grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
                <div v-for="f in facts" :key="f.key">
                  <dt class="text-muted-foreground">{{ f.label }}</dt>
                  <dd class="m-0 font-medium" :data-testid="`fact-${f.key}`">{{ f.value }}</dd>
                </div>
              </dl>
            </CardContent>
          </Card>

          <Card aria-labelledby="tenant-quota-title" data-testid="tenant-quotas">
            <CardHeader class="flex-row items-center justify-between gap-3">
              <CardTitle id="tenant-quota-title">{{ t('platform.tenant.quotas.title') }}</CardTitle>
              <Button variant="ghost" size="sm" data-testid="quotas-set" @click="openChange('quotas')">{{ t('platform.tenant.quotas.set') }}</Button>
            </CardHeader>
            <CardContent class="grid gap-4">
              <div v-for="m in meters" :key="m.what">
                <QuotaMeter :what="m.what" :label="t(`platform.tenant.quotas.${m.what}` as never)" :used="m.used" :limit="m.limit" />
                <Badge v-if="m.override" variant="secondary" class="mt-1.5" :data-testid="`override-${m.what}`">{{ t('platform.tenant.quotas.tenantSpecific', { plan: m.planText }) }}</Badge>
              </div>
            </CardContent>
          </Card>

          <Card aria-labelledby="tenant-mod-title" data-testid="tenant-modules">
            <CardHeader><CardTitle id="tenant-mod-title">{{ t('platform.tenant.modules.title') }}</CardTitle></CardHeader>
            <CardContent>
              <ul class="m-0 grid list-none gap-2 p-0 sm:grid-cols-2 lg:grid-cols-3">
                <li class="flex items-center justify-between gap-2 rounded-md border border-border px-3 py-2 text-sm" data-testid="module-CORE">
                  <span class="font-medium">{{ t('subscription.module.CORE') }}</span>
                  <Badge variant="success">{{ t('platform.tenant.modules.always') }}</Badge>
                </li>
                <li v-for="m in OPTIONAL" :key="m" class="flex items-center justify-between gap-2 rounded-md border border-border px-3 py-2 text-sm" :data-testid="`module-${m}`" :data-included="summary!.modules.includes(m)">
                  <span class="font-medium">{{ t(`subscription.module.${m}` as never) }}</span>
                  <Badge :variant="summary!.modules.includes(m) ? 'success' : 'outline'">{{ summary!.modules.includes(m) ? t('platform.tenant.modules.included') : t('platform.tenant.modules.notIncluded') }}</Badge>
                </li>
              </ul>
            </CardContent>
          </Card>

          <Card aria-labelledby="tenant-inv-title" class="min-w-0" data-testid="tenant-invoices">
            <CardHeader class="flex-row items-center justify-between gap-3">
              <CardTitle id="tenant-inv-title">{{ t('platform.tenant.invoices.title') }}</CardTitle>
              <Button variant="ghost" size="sm" data-testid="invoices-issue" @click="issueOpen = true">{{ t('platform.tenant.invoices.issue') }}</Button>
            </CardHeader>
            <CardContent>
              <ErrorNotice v-if="invoiceError" :error="invoiceError" inline />
              <DataTable v-else :columns="columns" :rows="invoices ?? []" row-key="id" :loading="invoices === null" :caption="t('platform.tenant.invoices.title')" :empty-title="t('platform.tenant.invoices.empty')" :row-test-id="(r) => `invoice-${r.number}`">
                <template #cell-number="{ row }"><span class="whitespace-nowrap">{{ row.number }}</span></template>
                <template #cell-period="{ row }">{{ formatDate(row.period_start) }} – {{ formatDate(row.period_end) }}</template>
                <template #cell-amount="{ row }"><span class="whitespace-nowrap tabular-nums">{{ money(row.currency_code, row.amount) }}</span></template>
                <template #cell-status="{ row }"><Badge :variant="stateVariant[stateOf(row)]">{{ t(`platform.tenant.invoices.state.${stateOf(row)}` as never) }}</Badge></template>
                <template #cell-due_on="{ row }">{{ formatDate(row.due_on) }}</template>
                <template #cell-ops="{ row }">
                  <div v-if="row.status === 'ISSUED'" class="flex flex-wrap gap-1">
                    <Button size="sm" variant="outline" data-testid="invoice-pay" @click="payFor = row">{{ t('platform.tenant.invoices.recordPayment') }}</Button>
                    <Button size="sm" variant="ghost" data-testid="invoice-void" @click="voidFor = row">{{ t('platform.tenant.invoices.void') }}</Button>
                  </div>
                  <span v-else-if="row.status === 'PAID'" class="text-sm text-muted-foreground" data-testid="invoice-note">{{ row.payment_reference ? t('platform.tenant.invoices.paidOn', { date: formatDate(row.paid_on), reference: row.payment_reference }) : t('platform.tenant.invoices.paidOnly', { date: formatDate(row.paid_on) }) }}</span>
                  <span v-else class="text-sm text-muted-foreground" data-testid="invoice-note">{{ t('platform.tenant.invoices.voidReason', { reason: row.void_reason ?? '' }) }}</span>
                </template>
              </DataTable>
            </CardContent>
          </Card>
        </div>

        <aside class="grid min-w-0 content-start gap-4">
          <Card aria-labelledby="tenant-adm-title" data-testid="tenant-admins">
            <CardHeader><CardTitle id="tenant-adm-title">{{ t('platform.tenant.admins.title') }}</CardTitle></CardHeader>
            <CardContent>
              <ErrorNotice v-if="adminError" :error="adminError" inline />
              <Skeleton v-else-if="admins === null" class="h-16 w-full" />
              <p v-else-if="admins.length === 0" class="m-0 text-sm text-muted-foreground" data-testid="admins-empty">{{ t('platform.tenant.admins.empty') }}</p>
              <ul v-else class="m-0 grid list-none gap-3 p-0">
                <li v-for="a in admins" :key="a.email" class="grid text-sm" data-testid="admin-row">
                  <span class="font-medium">{{ a.full_name }} <Badge v-if="!a.is_active" variant="outline">{{ t('platform.tenant.admins.inactive') }}</Badge></span>
                  <a :href="`mailto:${a.email}`" class="min-w-0 break-all text-primary underline-offset-4 hover:underline">{{ a.email }}</a>
                  <span class="text-muted-foreground">{{ a.last_login_at ? t('platform.tenant.admins.lastLogin', { date: formatDateTime(a.last_login_at) }) : t('platform.tenant.admins.never') }}</span>
                </li>
              </ul>
              <p class="m-0 mt-3 text-sm text-muted-foreground" data-testid="admins-note">{{ t('platform.tenant.admins.note') }}</p>
            </CardContent>
          </Card>

          <Card aria-labelledby="tenant-act-title" data-testid="tenant-activity">
            <CardHeader><CardTitle id="tenant-act-title">{{ t('platform.tenant.activity.title') }}</CardTitle></CardHeader>
            <CardContent>
              <ErrorNotice v-if="activityError" :error="activityError" inline />
              <Skeleton v-else-if="activity === null" class="h-16 w-full" />
              <p v-else-if="activity.length === 0" class="m-0 text-sm text-muted-foreground" data-testid="activity-empty">{{ t('platform.tenant.activity.empty') }}</p>
              <ol v-else class="m-0 grid list-none gap-3 p-0">
                <li v-for="a in activity" :key="a.id" class="grid text-sm" data-testid="activity-row">
                  <span>{{ whatOf(a) }}</span>
                  <span class="text-muted-foreground">{{ t('platform.tenant.activity.byWhen', { who: whoOf(a), when: formatDateTime(a.at) }) }}</span>
                </li>
              </ol>
              <Button v-if="activity && activity.length >= FEW && activityLimit < ALL" variant="link" class="mt-2 px-0" data-testid="activity-all" @click="seeAll">{{ t('platform.tenant.activity.seeAll') }}</Button>
            </CardContent>
          </Card>
        </aside>
      </div>

      <ChangeSubscriptionDialog v-model:open="changeOpen" :tenant="tenant" :plans="plans ?? []" :focus="changeFocus" @saved="load" />
      <IssueInvoiceDialog v-model:open="issueOpen" :tenant-code="tenant.code" :current-plan="tenant.summary.plan_code" :plans="plans ?? []" @issued="load" />
      <SuspendTenantDialog v-model:open="suspendOpen" mode="suspend" :tenant-code="tenant.code" :tenant-name="tenant.name" @done="load" />
      <SuspendTenantDialog v-model:open="reactivateOpen" mode="reactivate" :tenant-code="tenant.code" :tenant-name="tenant.name" @done="load" />
      <RecordPaymentDialog v-if="payFor" :open="true" :invoice="payFor" @update:open="(v) => { if (!v) payFor = null }" @paid="payFor = null; load()" />
      <VoidInvoiceDialog v-if="voidFor" :open="true" :invoice="voidFor" @update:open="(v) => { if (!v) voidFor = null }" @voided="voidFor = null; load()" />
    </template>
  </div>
</template>
