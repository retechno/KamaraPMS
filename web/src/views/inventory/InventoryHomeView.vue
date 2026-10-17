<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryDashboard, InventoryFbPart } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney, formatPercent } from '@/utils/format'

/**
 * The inventory page (docs/architecture/35-inventory.md 12): a few figures that say whether the stock is in order today (its value, the food and beverage cost of the month against the target, stock below zero,
 * items below their minimum, documents waiting for a decision) and the list of reports, each one a page of its own.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const data = ref<InventoryDashboard | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    data.value = (await api.GET('/api/v1/properties/{propertyId}/inventory/dashboard', { params: { path: { propertyId } } })).data ?? null
  } catch (e) {
    data.value = null
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const pct = (v: string | null | undefined): string => (v === null || v === undefined ? '–' : formatPercent(v))
const tone = (p: InventoryFbPart | undefined): 'default' | 'success' | 'danger' => (!p?.pct || !p.target_pct ? 'default' : Number(p.pct) > Number(p.target_pct) ? 'danger' : 'success')
const hint = (p: InventoryFbPart | undefined): string => {
  if (!p?.pct || !p.target_pct) return t('inventory.reports.noTarget')
  const diff = Number(p.pct) - Number(p.target_pct)
  return t(diff > 0 ? 'inventory.reports.pointsAbove' : 'inventory.reports.pointsBelow', { target: formatPercent(p.target_pct), points: Math.abs(diff).toFixed(1) })
}

interface Entry { id: string; to: string }
const groups: { id: string; entries: Entry[] }[] = [
  { id: 'cost', entries: [
    { id: 'daily', to: '/inventory/reports/daily-cost' }, { id: 'outlet', to: '/inventory/reports/outlet-cost' }, { id: 'recon', to: '/inventory/reports/fb-reconciliation' },
  ] },
  { id: 'stock', entries: [
    { id: 'card', to: '/inventory/costing' }, { id: 'value', to: '/inventory/reports/stock-value' }, { id: 'low', to: '/inventory/reports/low-stock' }, { id: 'neg', to: '/inventory/reports/negative-stock' },
  ] },
  { id: 'buy', entries: [{ id: 'grni', to: '/inventory/reports/received-not-invoiced' }, { id: 'usage', to: '/inventory/reports/usage' }, { id: 'gap', to: '/inventory/pos-gap' }] },
]
</script>

<template>
  <PageHeader :title="t('inventory.home.title')" :description="t('inventory.home.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <section v-if="data" aria-labelledby="dash-h" class="mb-6" data-testid="dashboard">
      <h2 id="dash-h" class="m-0 mb-2 text-sm font-semibold">{{ t('inventory.home.today', { date: formatDate(data.business_date) }) }}</h2>
      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <KpiCard :label="t('inventory.home.stockValue')" :value="formatMoney(data.stock_value)" data-testid="dash-value">
          <RouterLink to="/inventory/reports/stock-value" class="underline">{{ t('inventory.home.againstGl') }}</RouterLink>
        </KpiCard>
        <KpiCard :label="t('inventory.home.foodCost')" :value="pct(data.food.pct)" :tone="tone(data.food)" :hint="hint(data.food)" data-testid="dash-food" />
        <KpiCard :label="t('inventory.home.beverageCost')" :value="pct(data.beverage.pct)" :tone="tone(data.beverage)" :hint="hint(data.beverage)" data-testid="dash-beverage" />
        <KpiCard :label="t('inventory.home.negative')" :value="data.negative_items" :tone="data.negative_items ? 'danger' : 'default'" data-testid="dash-negative">
          <RouterLink to="/inventory/reports/negative-stock" class="underline">{{ data.negative_items ? t('inventory.home.negativeValue', { amount: formatMoney(data.negative_value) }) : t('inventory.home.none') }}</RouterLink>
        </KpiCard>
        <KpiCard :label="t('inventory.home.belowMin')" :value="data.below_min" :tone="data.below_min ? 'warning' : 'default'" data-testid="dash-below">
          <RouterLink to="/inventory/reports/low-stock" class="underline">{{ t('inventory.home.seeList') }}</RouterLink>
        </KpiCard>
        <KpiCard :label="t('inventory.home.waiting')" :value="data.waiting_approvals" data-testid="dash-waiting">
          <RouterLink to="/inventory/purchasing" class="underline">{{ t('inventory.home.seeApprovals') }}</RouterLink>
        </KpiCard>
      </div>
      <p class="m-0 mt-2 text-xs text-muted-foreground">{{ t('inventory.home.mtdNote', { from: formatDate(data.mtd_from) }) }}</p>
    </section>

    <section aria-labelledby="reports-h" data-testid="reports-list">
      <h2 id="reports-h" class="m-0 mb-2 text-sm font-semibold">{{ t('inventory.home.reports') }}</h2>
      <div class="flex flex-col gap-4">
        <div v-for="g in groups" :key="g.id">
          <h3 class="m-0 mb-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t(`inventory.home.group.${g.id}`) }}</h3>
          <ul class="m-0 grid list-none gap-3 p-0 sm:grid-cols-2 xl:grid-cols-3">
            <li v-for="e in g.entries" :key="e.id">
              <RouterLink :to="e.to" class="flex h-full flex-col gap-0.5 rounded-xl border border-border bg-card p-3 hover:bg-muted/40" :data-testid="`report-${e.id}`">
                <strong class="text-sm">{{ t(`inventory.home.report.${e.id}`) }}</strong>
                <span class="text-xs text-muted-foreground">{{ t(`inventory.home.report.${e.id}Hint`) }}</span>
              </RouterLink>
            </li>
          </ul>
        </div>
      </div>
    </section>
  </template>
</template>
