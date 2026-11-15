<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { platformApi } from '@/api/platformClient'
import { platformFetch } from '@/api/platformSession'
import { ApiError, toApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import AccessBadge from '@/components/app/AccessBadge.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FilterBar from '@/components/app/FilterBar.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import NewTenantDialog from '@/components/platform/NewTenantDialog.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { toast } from '@/composables/useToast'
import { currentLocale, t } from '@/i18n'
import { formatDate } from '@/utils/format'
import { platformMoney } from '@/utils/platformMoney'

/**
 * P4, the tenants (docs/architecture/39-platform-console.md 11): the summary, the filters (kept in the address: `?q=&status=&plan=`, so a link keeps them), the table, the export and
 * "New tenant". The server sorts (what needs action first) and answers everything in one list, so there is no paging.
 */
type Row = components['schemas']['PlatformTenantRow']
type Summary = components['schemas']['PlatformTenantSummary']
type State = Row['state']
const STATES: State[] = ['active', 'trial', 'grace', 'read_only', 'suspended']

const route = useRoute()
const router = useRouter()

const asText = (v: unknown): string => (typeof v === 'string' ? v : '')
const q = ref(asText(route.query.q))
const status = ref(asText(route.query.status))
const plan = ref(asText(route.query.plan))

const rows = ref<Row[]>([])
const summary = ref<Summary | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const planCodes = ref<string[]>([])
const newOpen = ref(false)
const exporting = ref(false)

/** The filters that are on, as the API takes them. */
function params(): { q?: string; status?: State; plan?: string } {
  const out: { q?: string; status?: State; plan?: string } = {}
  const text = asText(route.query.q).trim()
  const st = asText(route.query.status)
  const pl = asText(route.query.plan)
  if (text) out.q = text
  if ((STATES as string[]).includes(st)) out.status = st as State
  if (pl) out.plan = pl
  return out
}

let sequence = 0
async function load(): Promise<void> {
  const mine = ++sequence
  error.value = null
  try {
    const { data } = await platformApi.GET('/api/v1/platform/tenants', { params: { query: params() } })
    if (mine !== sequence) return // a newer search is on its way
    rows.value = data?.items ?? []
    summary.value = data?.summary ?? null
  } catch (e) {
    if (mine !== sequence) return
    error.value = e instanceof ApiError ? e : null
  } finally {
    if (mine === sequence) loaded.value = true
  }
}

async function loadPlans(): Promise<void> {
  try {
    const { data } = await platformApi.GET('/api/v1/platform/plans')
    planCodes.value = (data?.items ?? []).map((p) => p.code)
  } catch {
    planCodes.value = [] // the choices of the filter are only a help: the codes of the rows below are used then
  }
}

onMounted(() => {
  void load()
  void loadPlans()
})
// The address is the filter: a link from the plans screen, "back" and the form below all change it, and the list follows.
watch(
  () => route.query,
  () => {
    q.value = asText(route.query.q)
    status.value = asText(route.query.status)
    plan.value = asText(route.query.plan)
    void load()
  },
)

function apply(): void {
  const query: Record<string, string> = {}
  if (q.value.trim()) query.q = q.value.trim()
  if (status.value) query.status = status.value
  if (plan.value) query.plan = plan.value
  void router.replace({ path: route.path, query })
}

const planOptions = computed(() => {
  const codes = new Set(planCodes.value)
  for (const r of rows.value) codes.add(r.plan_code)
  if (plan.value) codes.add(plan.value)
  return [...codes].sort()
})
const activeFilters = computed(() => (status.value ? 1 : 0) + (plan.value ? 1 : 0))
const filtered = computed(() => Object.keys(params()).length > 0)

const openTotal = computed(() => {
  const totals = summary.value?.open_invoice_totals ?? []
  return totals.length ? totals.map((c) => platformMoney(c.currency_code, c.total)).join(' + ') : t('platform.tenants.none')
})
const kpis = computed(() => {
  const s = summary.value
  if (!s) return []
  return [
    { key: 'active', label: t('platform.tenants.kpi.active'), value: s.active, hint: t('platform.tenants.kpi.activeHint'), tone: 'default' as const },
    { key: 'trial', label: t('platform.tenants.kpi.trial'), value: s.trial, hint: t('platform.tenants.kpi.trialHint', { n: s.trial_ending }), tone: 'default' as const },
    { key: 'grace', label: t('platform.tenants.kpi.grace'), value: s.grace, hint: t('platform.tenants.kpi.graceHint'), tone: 'warning' as const },
    { key: 'read_only', label: t('platform.tenants.kpi.readOnly'), value: s.read_only, hint: t('platform.tenants.kpi.readOnlyHint'), tone: 'danger' as const },
    {
      key: 'open',
      label: t('platform.tenants.kpi.open'),
      value: openTotal.value,
      hint: s.open_invoice_count ? t('platform.tenants.kpi.openHint', { count: s.open_invoice_count, overdue: s.overdue_invoice_count }) : t('platform.tenants.kpi.openNone'),
      tone: 'default' as const,
    },
  ]
})

const columns = computed<Column<Row>[]>(() => [
  { key: 'name', label: t('platform.tenants.col.tenant') },
  { key: 'plan_code', label: t('platform.tenants.col.plan') },
  { key: 'state', label: t('platform.tenants.col.status') },
  { key: 'paid_through', label: t('platform.tenants.col.paidThrough') },
  { key: 'rooms', label: t('platform.tenants.col.rooms') },
  { key: 'users', label: t('platform.tenants.col.users') },
  { key: 'open', label: t('platform.tenants.col.open') },
  { key: 'ops', label: t('platform.tenants.col.actions') },
])

/** The line under the paid-through date: what the access state means in days. */
function hint(r: Row): string {
  if (r.state === 'suspended') return t('platform.tenants.hint.blocked')
  if (r.days_left === null) return ''
  if (r.state === 'grace') return t('platform.tenants.hint.graceLeft', { n: r.days_left })
  if (r.state === 'trial') return t('platform.tenants.hint.trialLeft', { n: r.days_left })
  return t('platform.tenants.hint.left', { n: r.days_left })
}
const roomsBar = (r: Row): string => (r.limits.max_rooms !== null && r.usage.rooms >= r.limits.max_rooms ? 'bg-destructive' : r.limits.max_rooms !== null && r.usage.rooms >= r.limits.max_rooms * 0.85 ? 'bg-warning' : 'bg-primary')
const hintTone = (r: Row): string => (r.state === 'read_only' || r.state === 'suspended' ? 'text-destructive' : r.state === 'grace' ? 'text-warning-text' : 'text-muted-foreground')
const tenantPath = (r: Row): string => `/platform/tenants/${encodeURIComponent(r.code)}`

const countLabel = computed(() => (rows.value.length === 1 ? t('platform.tenants.countOne') : t('platform.tenants.count', { n: rows.value.length })))

/** The CSV is fetched through the console's own client (the token is in memory, so a plain link cannot carry it) and saved from an object URL. */
async function exportCsv(): Promise<void> {
  if (exporting.value) return
  exporting.value = true
  try {
    const url = new URL('/api/v1/platform/tenants/export.csv', window.location.origin)
    for (const [k, v] of Object.entries(params())) url.searchParams.set(k, v)
    url.searchParams.set('lang', currentLocale())
    const res = await platformFetch(new Request(url.href, { headers: { Accept: 'text/csv' } }))
    if (!res.ok) {
      let problem: unknown
      try {
        problem = await res.json()
      } catch {
        problem = undefined
      }
      throw toApiError(problem, res.status)
    }
    const name = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? 'tenants.csv'
    const objectUrl = URL.createObjectURL(new Blob([await res.arrayBuffer()], { type: 'text/csv' }))
    const a = document.createElement('a')
    a.href = objectUrl
    a.download = name
    document.body.appendChild(a)
    a.click()
    a.remove()
    window.setTimeout(() => URL.revokeObjectURL(objectUrl), 60 * 1000)
  } catch (e) {
    if (e instanceof ApiError) toast.fromError(e)
    else toast.error(t('platform.tenants.exportFailed'))
  } finally {
    exporting.value = false
  }
}

function onCreated(code: string): void {
  void router.push(`/platform/tenants/${encodeURIComponent(code)}`)
}
</script>

<template>
  <PageHeader :title="t('platform.nav.tenants')" :description="t('platform.tenants.description')">
    <template #actions>
      <Button variant="outline" :disabled="exporting" data-testid="export-csv" @click="exportCsv">{{ t('platform.tenants.exportCsv') }}</Button>
      <Button data-testid="new-tenant" @click="newOpen = true">{{ t('platform.tenants.newTenant') }}</Button>
    </template>
  </PageHeader>

  <ErrorNotice :error="error" inline class="mb-3" data-testid="tenants-error" />

  <ul v-if="kpis.length" class="m-0 mb-4 grid list-none grid-cols-2 gap-3 p-0 lg:grid-cols-5" :aria-label="t('platform.tenants.summary')" data-testid="tenants-kpis">
    <li v-for="k in kpis" :key="k.key" :data-testid="`kpi-${k.key}`">
      <KpiCard :label="k.label" :value="k.value" :hint="k.hint" :tone="k.tone === 'default' || k.value === 0 ? 'default' : k.tone" class="h-full" />
    </li>
  </ul>

  <FilterBar :active="activeFilters" class="mb-4 flex flex-wrap items-end gap-3" data-testid="tenants-filters" @submit="apply">
    <template #search>
      <div class="flex min-w-[14rem] flex-1 flex-col gap-1.5">
        <label for="tenants-search" class="text-sm font-medium">{{ t('platform.tenants.filters.search') }}</label>
        <Input id="tenants-search" v-model="q" type="search" name="q" :placeholder="t('platform.tenants.filters.searchPlaceholder')" />
      </div>
    </template>
    <div class="flex flex-col gap-1.5">
      <label for="tenants-status" class="text-sm font-medium">{{ t('platform.tenants.filters.status') }}</label>
      <NativeSelect id="tenants-status" v-model="status" name="status" @change="apply">
        <option value="">{{ t('platform.tenants.filters.all') }}</option>
        <option v-for="s in STATES" :key="s" :value="s">{{ t(`platform.tenants.state.${s}` as never) }}</option>
      </NativeSelect>
    </div>
    <div class="flex flex-col gap-1.5">
      <label for="tenants-plan" class="text-sm font-medium">{{ t('platform.tenants.filters.plan') }}</label>
      <NativeSelect id="tenants-plan" v-model="plan" name="plan" @change="apply">
        <option value="">{{ t('platform.tenants.filters.all') }}</option>
        <option v-for="p in planOptions" :key="p" :value="p">{{ p }}</option>
      </NativeSelect>
    </div>
  </FilterBar>

  <section data-testid="tenants-list">
    <DataTable
      :columns="columns"
      :rows="rows"
      row-key="code"
      :loading="!loaded"
      :caption="t('platform.tenants.caption')"
      :empty-title="filtered ? t('platform.tenants.emptyFiltered') : t('platform.tenants.emptyTitle')"
      :empty-description="filtered ? '' : t('platform.tenants.emptyDescription')"
      :row-test-id="(r) => `tenant-${r.code}`"
    >
      <template #cell-name="{ row }">
        <div class="flex flex-col">
          <RouterLink :to="tenantPath(row)" class="font-medium text-primary hover:underline" data-testid="tenant-link">{{ row.name }}</RouterLink>
          <span class="text-xs text-muted-foreground">{{ row.code }}</span>
        </div>
      </template>
      <template #cell-state="{ row }">
        <Badge v-if="row.state === 'suspended'" variant="outline" data-testid="state-badge">{{ t('platform.tenants.state.suspended') }}</Badge>
        <AccessBadge v-else :access="row.access" :status="row.subscription_status" />
      </template>
      <template #cell-paid_through="{ row }">
        <div class="flex flex-col">
          <span class="whitespace-nowrap">{{ row.paid_through ? formatDate(row.paid_through) : t('platform.tenants.noEnd') }}</span>
          <span v-if="hint(row)" :class="['text-xs', hintTone(row)]" data-testid="paid-hint">{{ hint(row) }}</span>
        </div>
      </template>
      <template #cell-rooms="{ row }">
        <div class="flex min-w-24 flex-col gap-1" data-testid="rooms-cell">
          <span class="whitespace-nowrap tabular-nums">{{ row.usage.rooms }} / {{ row.limits.max_rooms === null ? '∞' : row.limits.max_rooms }}</span>
          <div v-if="row.limits.max_rooms !== null" class="h-1 overflow-hidden rounded-full bg-muted" aria-hidden="true">
            <div :class="['h-full rounded-full', roomsBar(row)]" :style="{ width: `${Math.min(row.limits.max_rooms === 0 ? 1 : row.usage.rooms / row.limits.max_rooms, 1) * 100}%` }" />
          </div>
        </div>
      </template>
      <template #cell-users="{ row }">
        <span class="whitespace-nowrap tabular-nums" data-testid="users-cell">{{ row.usage.users }} / {{ row.limits.max_users === null ? '∞' : row.limits.max_users }}</span>
      </template>
      <template #cell-open="{ row }">
        <div v-if="row.open_invoice_count > 0" class="flex flex-col" :class="row.overdue_invoice_count > 0 ? 'text-destructive' : ''" data-testid="open-cell">
          <span class="font-medium">{{ t('platform.tenants.rowOpen', { n: row.open_invoice_count }) }}</span>
          <span v-if="row.overdue_invoice_count > 0" class="text-xs">{{ t('platform.tenants.rowOverdue', { n: row.overdue_invoice_count }) }}</span>
        </div>
        <span v-else class="text-muted-foreground">{{ t('platform.tenants.none') }}</span>
      </template>
      <template #cell-ops="{ row }">
        <Button as-child size="sm" variant="outline">
          <RouterLink :to="tenantPath(row)" :aria-label="t('platform.tenants.openTenant', { name: row.name })" data-testid="open-tenant">{{ t('platform.tenants.open') }}</RouterLink>
        </Button>
      </template>
      <template #footer>
        <p v-if="loaded" class="m-0 mt-2 text-sm text-muted-foreground" data-testid="tenants-count">{{ countLabel }}</p>
        <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('platform.tenants.order') }}</p>
      </template>
    </DataTable>
  </section>

  <NewTenantDialog v-model:open="newOpen" @created="onCreated" />
</template>
