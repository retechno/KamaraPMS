<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryLowStockRow } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { downloadCsv } from '@/views/accounting/reportApi'
import { formatMoney } from '@/utils/format'
import { formatQty } from '@/utils/inventory'

/** Items below the minimum of their warehouse (docs/architecture/35-inventory.md 12.5): how far below, and the quantity to reorder; a purchase request can be started from here. */
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const rows = ref<InventoryLowStockRow[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const canRequest = computed(() => auth.can('inventory.request', pid.value))

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/low-stock', { params: { path: { propertyId } } })
    rows.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
async function csv(): Promise<void> {
  if (pid.value !== null) await downloadCsv('/api/v1/properties/{propertyId}/inventory/reports/low-stock', { path: { propertyId: pid.value } }, 'low-stock.csv')
}
watch(pid, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('inventory.reports.low.title')" :description="t('inventory.reports.low.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <div class="mb-4 flex flex-wrap items-center justify-end gap-2">
      <Button v-if="canRequest" as-child variant="outline" size="sm"><RouterLink to="/inventory/requests/new" data-testid="low-request">{{ t('inventory.reports.low.request') }}</RouterLink></Button>
      <Button type="button" variant="outline" size="sm" data-testid="report-csv" @click="csv">{{ t('inventory.reports.csv') }}</Button>
    </div>
    <p v-if="loaded && rows.length === 0" class="m-0 text-sm text-muted-foreground" data-testid="low-empty">{{ t('inventory.reports.low.empty') }}</p>
    <div v-else-if="rows.length" class="overflow-x-auto">
      <table class="w-full min-w-[44rem] text-sm" data-testid="low-table">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.value.warehouse') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.low.qty') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.low.min') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.low.missing') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.low.reorder') }}</th>
            <th class="py-1 text-right font-medium">{{ t('inventory.reports.value.value') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="x in rows" :key="`${x.warehouse_id}-${x.item_id}`" class="border-t border-border" :data-testid="`low-${x.warehouse_code}-${x.item_code}`">
            <td class="py-2 pr-2">{{ x.warehouse_code }}</td><td class="py-2 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }}</span></td>
            <td class="py-2 pr-2 text-right" :class="Number(x.qty) <= 0 ? 'font-semibold text-destructive' : ''">{{ formatQty(x.qty) }} {{ x.unit_code }}</td>
            <td class="py-2 pr-2 text-right">{{ formatQty(x.min_qty) }}</td><td class="py-2 pr-2 text-right font-semibold">▼ {{ formatQty(x.missing) }}</td>
            <td class="py-2 pr-2 text-right">{{ Number(x.reorder_qty) ? formatQty(x.reorder_qty) : '–' }}</td><td class="py-2 text-right">{{ formatMoney(x.value) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </template>
</template>
