<script setup lang="ts">
import { watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryValueReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import { Badge } from '@/components/ui/badge'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { formatDate, formatMoney } from '@/utils/format'

/**
 * The value of the stock as of a day (docs/architecture/35-inventory.md 12.5, 6.3): by warehouse and category, and beside the inventory accounts of the general ledger (1310-1340) with the status. The open day is
 * read from the balances, a closed day from the snapshot its close took.
 */
const r = useInventoryReport<InventoryValueReport>('/api/v1/properties/{propertyId}/inventory/reports/stock-value', 'stock-value.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/stock-value', { params: { path: { propertyId }, query: p.to ? { as_of: p.to } : {} } })
  return data ?? null
}, { single: true })
watch(r.pid, r.load, { immediate: true })
const money = (v: string): string => (Number(v) === 0 ? '0' : formatMoney(v))
</script>

<template>
  <PageHeader :title="t('inventory.reports.value.title')" :description="t('inventory.reports.value.intro')" />
  <ReportPrintHeader :to="r.period.to" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:to="r.period.to" single :busy="r.busy.value" @show="r.load" @csv="r.csv()" />
    <div v-if="r.data.value" class="flex flex-col gap-5">
      <p class="m-0 flex flex-wrap items-center gap-2 text-sm">
        <strong>{{ t('inventory.reports.value.asOf', { date: formatDate(r.data.value.as_of) }) }}</strong>
        <Badge variant="outline">{{ t(r.data.value.live ? 'inventory.reports.value.live' : 'inventory.reports.value.snapshot') }}</Badge>
      </p>

      <section aria-labelledby="gl-acc-h" data-testid="value-accounts">
        <div class="mb-2 flex flex-wrap items-center gap-2">
          <h2 id="gl-acc-h" class="m-0 text-sm font-semibold">{{ t('inventory.reports.value.accounts') }}</h2>
          <Badge :variant="r.data.value.status === 'MATCH' ? 'success' : r.data.value.status === 'DIFFERENCE' ? 'destructive' : 'outline'" data-testid="value-status">
            {{ r.data.value.status === 'MATCH' ? '✓ ' : r.data.value.status === 'DIFFERENCE' ? '✕ ' : '' }}{{ t(`inventory.reports.gl.${r.data.value.status}`) }}
          </Badge>
          <span v-if="r.data.value.gl_through" class="text-xs text-muted-foreground">{{ t('inventory.reports.gl.through', { date: formatDate(r.data.value.gl_through) }) }}</span>
        </div>
        <p v-if="r.data.value.status === 'PENDING'" class="m-0 text-sm text-muted-foreground">{{ t('inventory.reports.gl.pending') }}</p>
        <div v-else class="overflow-x-auto">
          <table class="w-full min-w-[30rem] text-sm">
            <thead>
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.gl.account') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.gl.stock') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.gl.ledger') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.gl.difference') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="a in r.data.value.accounts" :key="a.account_id" class="border-t border-border" :data-testid="`account-${a.code}`">
                <td class="py-1.5 pr-2">{{ a.code }} · {{ a.name }}</td><td class="py-1.5 pr-2 text-right">{{ money(a.stock) }}</td><td class="py-1.5 pr-2 text-right">{{ money(a.gl) }}</td>
                <td class="py-1.5 text-right" :class="Number(a.difference) !== 0 ? 'font-semibold text-destructive' : ''">{{ money(a.difference) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section aria-labelledby="by-wh-h">
        <h2 id="by-wh-h" class="m-0 mb-2 text-sm font-semibold">{{ t('inventory.reports.value.byWarehouse') }}</h2>
        <p v-if="!r.data.value.warehouses.length" class="m-0 text-sm text-muted-foreground" data-testid="value-empty">{{ t('inventory.reports.empty') }}</p>
        <div v-else class="overflow-x-auto">
          <table class="w-full min-w-[30rem] text-sm" data-testid="value-table">
            <thead>
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.value.warehouse') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.value.category') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.value.items') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.value.value') }}</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="w in r.data.value.warehouses" :key="w.warehouse_id">
                <tr v-for="(c, i) in w.categories" :key="`${w.warehouse_id}-${c.category_id}`" class="border-t border-border" :data-testid="`value-${w.code}-${c.category_code}`">
                  <td class="py-1.5 pr-2">{{ i === 0 ? `${w.code} · ${w.name}` : '' }}</td><td class="py-1.5 pr-2">{{ c.category_name }}</td>
                  <td class="py-1.5 pr-2 text-right">{{ c.items }}</td><td class="py-1.5 text-right">{{ money(c.value) }}</td>
                </tr>
                <tr class="border-t border-border bg-muted/40 font-semibold"><td class="py-1.5 pr-2" colspan="3">{{ t('inventory.reports.value.warehouseTotal', { warehouse: w.code }) }}</td><td class="py-1.5 text-right">{{ money(w.total) }}</td></tr>
              </template>
              <tr class="border-t-2 border-border font-semibold" data-testid="value-total"><td class="py-1.5 pr-2" colspan="3">{{ t('inventory.reports.total') }}</td><td class="py-1.5 text-right">{{ money(r.data.value.total) }}</td></tr>
            </tbody>
          </table>
        </div>
      </section>

      <section v-if="r.data.value.categories.length" aria-labelledby="by-cat-h">
        <h2 id="by-cat-h" class="m-0 mb-2 text-sm font-semibold">{{ t('inventory.reports.value.byCategory') }}</h2>
        <ul class="m-0 grid list-none gap-2 p-0 sm:grid-cols-2 xl:grid-cols-4">
          <li v-for="c in r.data.value.categories" :key="c.category_id" class="rounded-xl border border-border bg-card p-3 text-sm" :data-testid="`category-${c.category_code}`">
            <span class="text-muted-foreground">{{ c.category_name }}</span><strong class="block text-lg">{{ money(c.value) }}</strong>
          </li>
        </ul>
      </section>
    </div>
  </template>
</template>
