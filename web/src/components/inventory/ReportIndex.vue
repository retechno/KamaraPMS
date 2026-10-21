<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { t } from '@/i18n'

/** The list of the reports of the inventory, in five groups (docs/architecture/35-inventory.md 22): shown on the inventory page and on the page "Inventory reports". */
interface Entry { id: string; to: string }
const groups: { id: string; entries: Entry[] }[] = [
  { id: 'stock', entries: [
    { id: 'card', to: '/inventory/costing' }, { id: 'value', to: '/inventory/reports/stock-value' }, { id: 'low', to: '/inventory/reports/low-stock' }, { id: 'neg', to: '/inventory/reports/negative-stock' },
  ] },
  { id: 'purchasing', entries: [
    { id: 'grni', to: '/inventory/reports/received-not-invoiced' }, { id: 'priceVar', to: '/inventory/reports/price-variances' },
  ] },
  { id: 'fb', entries: [
    { id: 'daily', to: '/inventory/reports/daily-cost' }, { id: 'outlet', to: '/inventory/reports/outlet-cost' }, { id: 'recon', to: '/inventory/reports/fb-reconciliation' }, { id: 'recipeCosts', to: '/inventory/reports/recipe-costs' },
  ] },
  { id: 'use', entries: [
    { id: 'usage', to: '/inventory/reports/usage' }, { id: 'waste', to: '/inventory/reports/waste' }, { id: 'meals', to: '/inventory/reports/meals' }, { id: 'amenities', to: '/inventory/reports/amenities-by-month' },
  ] },
  { id: 'control', entries: [
    { id: 'countVar', to: '/inventory/reports/count-variances' }, { id: 'trfNoSr', to: '/inventory/reports/transfers-without-requisition' }, { id: 'pending', to: '/inventory/reports/pending-approvals' }, { id: 'gap', to: '/inventory/pos-gap' },
  ] },
]
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="reports-index">
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
</template>
