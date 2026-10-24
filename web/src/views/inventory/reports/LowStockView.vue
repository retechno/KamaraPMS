<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryLowStockRow } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
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
const router = useRouter()
const selected = ref<string[]>([])
const clientUuid = ref(crypto.randomUUID())
const busy = ref(false)
const warning = ref('')
const keyOf = (x: InventoryLowStockRow): string => `${x.warehouse_id}-${x.item_id}`
const allSelected = computed(() => rows.value.length > 0 && selected.value.length === rows.value.length)
const toggleAll = (on: boolean): void => {
  selected.value = on ? rows.value.map(keyOf) : []
}
/** A purchase request is for one warehouse: the chosen rows must all be in the same one. */
async function makeRequest(): Promise<void> {
  const propertyId = pid.value
  const chosen = rows.value.filter((x) => selected.value.includes(keyOf(x)))
  warning.value = ''
  if (propertyId === null || chosen.length === 0) return
  if (new Set(chosen.map((x) => x.warehouse_id)).size > 1) {
    warning.value = t('inventory.reports.lowStock.oneWarehouse')
    return
  }
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/requests/from-low-stock', {
      params: { path: { propertyId } },
      body: { client_uuid: clientUuid.value, warehouse_id: chosen[0]!.warehouse_id, item_ids: chosen.map((x) => x.item_id), department_id: null, note: t('inventory.reports.lowStock.requestNote') },
    })
    clientUuid.value = crypto.randomUUID()
    if (data) await router.push(`/inventory/requests/${data.id}`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const canRequest = computed(() => auth.can('inventory.request', pid.value))

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/low-stock', { params: { path: { propertyId } } })
    rows.value = data?.data ?? []
    selected.value = []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
const print = (): void => window.print()
async function csv(): Promise<void> {
  if (pid.value !== null) await downloadCsv('/api/v1/properties/{propertyId}/inventory/reports/low-stock', { path: { propertyId: pid.value } }, 'low-stock.csv')
}
watch(pid, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('inventory.reports.low.title')" :description="t('inventory.reports.low.intro')" />
  <ReportPrintHeader />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <div class="mb-4 flex flex-wrap items-center justify-end gap-2 print:hidden">
      <span v-if="selected.length" class="text-sm text-muted-foreground print:hidden" data-testid="low-selected">{{ t('inventory.reports.lowStock.selected', { n: selected.length }) }}</span>
      <Button v-if="canRequest" type="button" size="sm" :disabled="busy || !selected.length" class="print:hidden" data-testid="low-make-pr" @click="makeRequest">{{ t('inventory.reports.lowStock.makePr') }}</Button>
      <Button v-if="canRequest" as-child variant="outline" size="sm"><RouterLink to="/inventory/requests/new" data-testid="low-request">{{ t('inventory.reports.low.request') }}</RouterLink></Button>
      <span class="flex gap-2 print:hidden"><Button type="button" variant="outline" size="sm" data-testid="report-print" @click="print">{{ t('inventory.reports.print') }}</Button><Button type="button" variant="outline" size="sm" data-testid="report-csv" @click="csv">{{ t('inventory.reports.csv') }}</Button></span>
    </div>
    <p v-if="warning" class="m-0 mb-3 text-sm text-destructive" role="alert" data-testid="low-warning">{{ warning }}</p>
    <p v-if="loaded && rows.length === 0" class="m-0 text-sm text-muted-foreground" data-testid="low-empty">{{ t('inventory.reports.low.empty') }}</p>
    <div v-else-if="rows.length" class="overflow-x-auto">
      <table class="w-full min-w-[44rem] text-sm" data-testid="low-table">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th v-if="canRequest" class="w-8 py-1 pr-2 font-medium print:hidden"><input type="checkbox" :checked="allSelected" :aria-label="t('inventory.reports.lowStock.selectAll')" data-testid="low-select-all" @change="toggleAll(($event.target as HTMLInputElement).checked)" /></th>
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.value.warehouse') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.low.qty') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.low.min') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.low.missing') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.low.reorder') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.lowStock.max') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.lowStock.suggest') }}</th>
            <th class="py-1 text-right font-medium">{{ t('inventory.reports.value.value') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="x in rows" :key="keyOf(x)" class="border-t border-border" :data-testid="`low-${x.warehouse_code}-${x.item_code}`">
            <td v-if="canRequest" class="py-2 pr-2 print:hidden"><input v-model="selected" type="checkbox" :value="keyOf(x)" :aria-label="t('inventory.reports.lowStock.select', { name: x.item_name })" :data-testid="`low-check-${x.warehouse_code}-${x.item_code}`" /></td>
            <td class="py-2 pr-2">{{ x.warehouse_code }}</td><td class="py-2 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }}</span></td>
            <td class="py-2 pr-2 text-right" :class="Number(x.qty) <= 0 ? 'font-semibold text-destructive' : ''">{{ formatQty(x.qty) }} {{ x.unit_code }}</td>
            <td class="py-2 pr-2 text-right">{{ formatQty(x.min_qty) }}</td><td class="py-2 pr-2 text-right font-semibold">▼ {{ formatQty(x.missing) }}</td>
            <td class="py-2 pr-2 text-right">{{ Number(x.reorder_qty) ? formatQty(x.reorder_qty) : '–' }}</td><td class="py-2 pr-2 text-right">{{ x.max_qty && Number(x.max_qty) ? formatQty(x.max_qty) : '–' }}</td><td class="py-2 pr-2 text-right font-semibold">{{ formatQty(x.suggest_qty) }} {{ x.unit_code }}</td><td class="py-2 text-right">{{ formatMoney(x.value) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </template>
</template>
