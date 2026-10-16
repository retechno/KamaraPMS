<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryUnit } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/** The units of measure (docs/architecture/35-inventory.md 3.2): a list that fills the screen; a unit opens its own page. */
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('inventory.manage', pid.value))
const units = ref<InventoryUnit[]>([])
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
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/units', { params: { path: { propertyId } } })
    units.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const columns = computed<Column<InventoryUnit>[]>(() => [
  { key: 'code', label: t('inventory.units.code'), class: 'font-medium' },
  { key: 'name', label: t('inventory.units.name') },
  { key: 'in_use', label: t('inventory.units.inUse') },
  { key: 'is_active', label: t('inventory.units.status') },
])
</script>

<template>
  <PageHeader :title="t('inventory.units.title')" :description="t('inventory.units.intro')">
    <template #actions>
      <Button v-if="canManage" as-child><RouterLink to="/inventory/units/new" data-testid="unit-new">{{ t('inventory.units.new') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <DataTable
    v-else :columns="columns" :rows="units" row-key="id" :loading="!loaded" :clickable="canManage" :empty-title="t('inventory.units.empty')" :empty-description="t('emptyState.inventoryUnits')"
    :row-to="(r) => (canManage ? `/inventory/units/${r.id}` : undefined)" :row-test-id="(r) => `unit-${r.code}`" data-testid="units-table"
  >
    <template #cell-in_use="{ row }">{{ row.in_use ? t('common.yes') : t('common.no') }}</template>
    <template #cell-is_active="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('common.active') : t('common.inactive') }}</Badge></template>
  </DataTable>
</template>
