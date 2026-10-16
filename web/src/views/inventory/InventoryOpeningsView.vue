<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryOpening } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/** The opening stock documents posted (docs/architecture/35-inventory.md 5.2): a list that fills the screen; a document opens its own page. */
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canOpen = computed(() => auth.can('inventory.opening', pid.value))
const openings = ref<InventoryOpening[]>([])
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
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/openings', { params: { path: { propertyId } } })
    openings.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const columns = computed<Column<InventoryOpening>[]>(() => [
  { key: 'number', label: t('inventory.openings.number'), class: 'font-medium' },
  { key: 'warehouse_name', label: t('inventory.openings.warehouse') },
  { key: 'business_date', label: t('inventory.openings.date'), format: 'date' },
  { key: 'line_count', label: t('inventory.openings.lines'), align: 'right' },
  { key: 'total_value', label: t('inventory.openings.total'), align: 'right', format: 'money' },
])
</script>

<template>
  <PageHeader :title="t('inventory.openings.title')" :description="t('inventory.openings.intro')">
    <template #actions>
      <Button v-if="canOpen" as-child><RouterLink to="/inventory/openings/new" data-testid="opening-new">{{ t('inventory.openings.new') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <DataTable
    v-else :columns="columns" :rows="openings" row-key="id" :loading="!loaded" clickable :empty-title="t('inventory.openings.empty')" :empty-description="t('emptyState.inventoryOpenings')"
    :row-to="(r) => `/inventory/openings/${r.id}`" :row-test-id="(r) => `opening-${r.number}`" data-testid="openings-table"
  />
</template>
