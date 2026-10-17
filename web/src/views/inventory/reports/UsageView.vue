<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '@/api/client'
import type { InventoryUsage } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPeriodBar from '@/components/inventory/ReportPeriodBar.vue'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { formatMoney } from '@/utils/format'
import { formatQty } from '@/utils/inventory'

/** What each department took from the stock in a period (docs/architecture/35-inventory.md 12.5): by kind (a use, waste, staff meals, entertainment), and by item with the quantity and the cost. */
const r = useInventoryReport<InventoryUsage>('/api/v1/properties/{propertyId}/inventory/reports/usage', 'usage.csv', async (propertyId, p) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/usage', { params: { path: { propertyId }, query: { from: p.from, to: p.to } } })
  return data ?? null
})
watch(r.pid, r.load, { immediate: true })
const open = ref<Record<string, boolean>>({})
const key = (id: number | null): string => String(id ?? 0)
</script>

<template>
  <PageHeader :title="t('inventory.reports.usage.title')" :description="t('inventory.reports.usage.intro')" />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <ReportPeriodBar v-model:from="r.period.from" v-model:to="r.period.to" :busy="r.busy.value" @show="r.load" @csv="r.csv()" />
    <p v-if="r.loaded.value && !r.data.value?.departments.length" class="m-0 text-sm text-muted-foreground" data-testid="usage-empty">{{ t('inventory.reports.empty') }}</p>
    <div v-else-if="r.data.value" class="flex flex-col gap-3">
      <section v-for="d in r.data.value.departments" :key="key(d.department_id)" class="rounded-xl border border-border bg-card p-3" :data-testid="`usage-${key(d.department_id)}`">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="m-0 text-sm font-semibold">{{ d.name || t('inventory.reports.noDepartment') }}</h2>
          <span class="text-sm font-semibold">{{ formatMoney(d.total) }}</span>
        </div>
        <ul class="m-0 mt-1 flex list-none flex-wrap gap-x-4 p-0 text-xs text-muted-foreground">
          <li v-for="(v, k) in d.by_kind" :key="k">{{ t(`inventory.reports.usage.kind.${k}`) }}: {{ formatMoney(v) }}</li>
        </ul>
        <button type="button" class="mt-2 text-xs underline" :aria-expanded="!!open[key(d.department_id)]" :data-testid="`usage-toggle-${key(d.department_id)}`" @click="open[key(d.department_id)] = !open[key(d.department_id)]">{{ t(open[key(d.department_id)] ? 'inventory.reports.usage.hide' : 'inventory.reports.usage.show', { n: d.items.length }) }}</button>
        <div v-if="open[key(d.department_id)]" class="mt-2 overflow-x-auto">
          <table class="w-full min-w-[34rem] text-sm">
            <thead><tr class="text-left text-xs text-muted-foreground"><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.usage.kindTitle') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.usage.qty') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.value.value') }}</th></tr></thead>
            <tbody>
              <tr v-for="(x, n) in d.items" :key="n" class="border-t border-border">
                <td class="py-1.5 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }} · {{ x.category_name }}</span></td>
                <td class="py-1.5 pr-2">{{ t(`inventory.reports.usage.kind.${x.kind}`) }}</td>
                <td class="py-1.5 pr-2 text-right">{{ formatQty(x.qty) }} {{ x.unit_code }}</td><td class="py-1.5 text-right">{{ formatMoney(x.value) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
      <p class="m-0 text-right text-sm font-semibold" data-testid="usage-total">{{ t('inventory.reports.total') }}: {{ formatMoney(r.data.value.total) }}</p>
    </div>
  </template>
</template>
