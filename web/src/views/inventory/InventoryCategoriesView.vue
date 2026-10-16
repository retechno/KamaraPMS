<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryCategory } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/** The categories and their accounts (docs/architecture/35-inventory.md 6.1): a list that fills the screen; a category opens its own page. */
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('inventory.manage', pid.value))
const categories = ref<InventoryCategory[]>([])
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
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/categories', { params: { path: { propertyId } } })
    categories.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const columns = computed<Column<InventoryCategory>[]>(() => [
  { key: 'code', label: t('inventory.categories.code'), class: 'font-medium' },
  { key: 'name', label: t('inventory.categories.name') },
  { key: 'kind', label: t('inventory.categories.kind') },
  { key: 'inventory_account_code', label: t('inventory.categories.inventoryAccount') },
  { key: 'cogs_account_code', label: t('inventory.categories.cogsAccount'), hideOnMobile: true },
  { key: 'issue_account_code', label: t('inventory.categories.issueAccount'), hideOnMobile: true },
  { key: 'item_count', label: t('inventory.categories.items'), align: 'right' },
  { key: 'is_active', label: t('inventory.categories.status') },
])
</script>

<template>
  <PageHeader :title="t('inventory.categories.title')" :description="t('inventory.categories.intro')">
    <template #actions>
      <Button v-if="canManage" as-child><RouterLink to="/inventory/categories/new" data-testid="category-new">{{ t('inventory.categories.new') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <DataTable
    v-else :columns="columns" :rows="categories" row-key="id" :loading="!loaded" :empty-title="t('inventory.categories.empty')" :empty-description="t('emptyState.inventoryCategories')"
    :row-to="(r) => (canManage ? `/inventory/categories/${r.id}` : undefined)" :row-test-id="(r) => `category-${r.code}`" data-testid="categories-table"
  >
    <template #cell-code="{ row }"><RouterLink v-if="canManage" :to="`/inventory/categories/${row.id}`" class="text-primary hover:underline">{{ row.code }}</RouterLink><template v-else>{{ row.code }}</template></template>
    <template #cell-kind="{ row }">{{ t(`inventory.categoryKind.${row.kind}`) }}</template>
    <template #cell-inventory_account_code="{ row }">{{ row.inventory_account_code }} <span class="text-xs text-muted-foreground">{{ row.inventory_account_name }}</span></template>
    <template #cell-cogs_account_code="{ row }">{{ row.cogs_account_code ?? '—' }}</template>
    <template #cell-issue_account_code="{ row }">{{ row.issue_account_code ?? '—' }}</template>
    <template #cell-is_active="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('common.active') : t('common.inactive') }}</Badge></template>
  </DataTable>
</template>
