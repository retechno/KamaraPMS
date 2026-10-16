<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryWarehouse } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/** The warehouses (docs/architecture/35-inventory.md 3.2): a list that fills the screen with the value of the stock of each; a warehouse opens its own page. */
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('inventory.manage', pid.value))
const warehouses = ref<InventoryWarehouse[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', { params: { path: { propertyId } } })
    warehouses.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const columns = computed<Column<InventoryWarehouse>[]>(() => [
  { key: 'code', label: t('inventory.warehouses.code'), class: 'font-medium' },
  { key: 'name', label: t('inventory.warehouses.name') },
  { key: 'kind', label: t('inventory.warehouses.kind') },
  { key: 'outlet_code', label: t('inventory.warehouses.outlet'), hideOnMobile: true },
  { key: 'department_code', label: t('inventory.warehouses.department'), hideOnMobile: true },
  { key: 'cost_class', label: t('inventory.warehouses.costClass') },
  { key: 'item_count', label: t('inventory.warehouses.items'), align: 'right' },
  { key: 'stock_value', label: t('inventory.warehouses.value'), align: 'right', format: 'money' },
  { key: 'is_active', label: t('inventory.warehouses.status') },
])
</script>

<template>
  <PageHeader :title="t('inventory.warehouses.title')" :description="t('inventory.warehouses.intro')">
    <template #actions>
      <Button v-if="canManage" as-child><RouterLink to="/inventory/warehouses/new" data-testid="warehouse-new">{{ t('inventory.warehouses.new') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <DataTable
    v-else :columns="columns" :rows="warehouses" row-key="id" :loading="!loaded" :clickable="canManage" :empty-title="t('inventory.warehouses.empty')" :empty-description="t('emptyState.inventoryWarehouses')"
    :row-to="(r) => (canManage ? `/inventory/warehouses/${r.id}` : undefined)" :row-test-id="(r) => `warehouse-${r.code}`" data-testid="warehouses-table"
  >
    <template #cell-kind="{ row }">{{ t(`inventory.warehouseKind.${row.kind}`) }}</template>
    <template #cell-outlet_code="{ row }">{{ row.outlet_code ?? '—' }}</template>
    <template #cell-department_code="{ row }">{{ row.department_code ?? '—' }}</template>
    <template #cell-cost_class="{ row }">{{ t(`inventory.costClass.${row.cost_class}`) }}</template>
    <template #cell-is_active="{ row }">
      <Badge v-if="row.frozen" variant="warning">{{ t('inventory.warehouses.frozen') }}</Badge>
      <Badge v-else :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('common.active') : t('common.inactive') }}</Badge>
    </template>
  </DataTable>
</template>
