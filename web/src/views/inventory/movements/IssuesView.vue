<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryCostByDepartment, InventoryIssueBrief } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'

/**
 * Use and waste (docs/architecture/35-inventory.md 5.4, 5.9, mockup 24): the issues to a department, the wastes, the staff meals, the entertainments and the automatic amenities documents of the closes, with the
 * cost of the month by department. A document opens its own page; a mistake is corrected there with a reversing document, never edited.
 */
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)

const rows = ref<InventoryIssueBrief[]>([])
const cost = ref<InventoryCostByDepartment[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const chip = ref('all')
const q = ref('')

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/issues', { params: { path: { propertyId }, query: { limit: 200 } } })
    rows.value = data?.data ?? []
    cost.value = data?.cost_by_department ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const chips = computed(() => [
  { key: 'all', label: t('inventory.issues.chipAll') },
  { key: 'USE', label: t('inventory.issues.purpose.USE') },
  { key: 'WASTE', label: t('inventory.issues.purpose.WASTE') },
  { key: 'STAFF_MEAL', label: t('inventory.issues.purpose.STAFF_MEAL') },
  { key: 'ENTERTAINMENT', label: t('inventory.issues.purpose.ENTERTAINMENT') },
  { key: 'AMENITY', label: t('inventory.issues.chipAmenity') },
])
const shown = computed(() =>
  rows.value.filter((r) => (chip.value === 'all' ? true : chip.value === 'AMENITY' ? r.origin === 'AMENITY' : r.purpose === chip.value && r.origin === 'MANUAL') && (!q.value.trim() || `${r.iss_number} ${r.department_name ?? ''} ${r.warehouse_name}`.toLowerCase().includes(q.value.trim().toLowerCase()))),
)

/** The cost of the month for a department (a card of the page), the purposes added up. */
const byDepartment = computed(() => {
  const m = new Map<string, number>()
  for (const c of cost.value) m.set(c.department_name || t('inventory.issues.noDepartment'), (m.get(c.department_name || t('inventory.issues.noDepartment')) ?? 0) + Number(c.value))
  return [...m.entries()].slice(0, 4)
})

const cols = computed<Column<InventoryIssueBrief>[]>(() => [
  { key: 'iss_number', label: t('inventory.issues.number'), class: 'font-medium whitespace-nowrap' },
  { key: 'purpose', label: t('inventory.issues.kind') },
  { key: 'business_date', label: t('inventory.issues.date'), format: 'date' },
  { key: 'department_name', label: t('inventory.issues.department') },
  { key: 'warehouse_name', label: t('inventory.issues.warehouse') },
  { key: 'line_count', label: t('inventory.issues.items'), align: 'right' },
  { key: 'total_value', label: t('inventory.issues.cost'), align: 'right', format: 'money' },
  { key: 'status', label: t('inventory.issues.status') },
])
</script>

<template>
  <PageHeader :title="t('inventory.issues.title')" :description="t('inventory.issues.intro')">
    <template #actions>
      <Button v-if="can('inventory.move')" as-child variant="outline"><RouterLink to="/inventory/issues/new?purpose=WASTE" data-testid="issue-waste">{{ t('inventory.issues.newWaste') }}</RouterLink></Button>
      <Button v-if="can('inventory.move')" as-child variant="outline"><RouterLink to="/inventory/issues/new?purpose=STAFF_MEAL" data-testid="issue-meal">{{ t('inventory.issues.newMeal') }}</RouterLink></Button>
      <Button v-if="can('inventory.move')" as-child variant="outline"><RouterLink to="/inventory/issues/new?purpose=ENTERTAINMENT" data-testid="issue-entertainment">{{ t('inventory.issues.newEntertainment') }}</RouterLink></Button>
      <Button v-if="can('inventory.move')" as-child><RouterLink to="/inventory/issues/new" data-testid="issue-new">{{ t('inventory.issues.newUse') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <template v-else>
    <div v-if="byDepartment.length" class="mb-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-4" data-testid="cost-cards">
      <div v-for="[name, value] in byDepartment" :key="name" class="rounded-xl border border-border bg-card p-3">
        <div class="text-xs text-muted-foreground">{{ t('inventory.issues.costOf', { department: name }) }}</div>
        <div class="text-lg font-semibold">{{ formatMoney(String(value)) }}</div>
      </div>
    </div>
    <div class="mb-3 flex flex-wrap items-center gap-2">
      <input v-model="q" type="search" name="q" class="h-9 w-full max-w-sm rounded-md border border-border bg-card px-3 text-sm" :placeholder="t('inventory.issues.search')" :aria-label="t('inventory.issues.search')" data-testid="issues-search" />
      <Button v-for="c in chips" :key="c.key" type="button" size="sm" class="rounded-full" :variant="chip === c.key ? 'default' : 'outline'" :data-testid="`chip-${c.key}`" @click="chip = c.key">{{ c.label }}</Button>
    </div>
    <DataTable :columns="cols" :rows="shown" row-key="id" :loading="!loaded" :empty-title="t('inventory.issues.empty')" :empty-description="t('emptyState.inventoryIssues')"
      :row-to="(r) => `/inventory/issues/${r.id}`" :row-test-id="(r) => `issue-${r.iss_number}`" data-testid="issues-table">
      <template #cell-iss_number="{ row }"><RouterLink :to="`/inventory/issues/${row.id}`" class="text-primary hover:underline">{{ row.iss_number }}</RouterLink></template>
      <template #cell-purpose="{ row }">{{ row.origin === 'AMENITY' ? t('inventory.issues.amenity') : t(`inventory.issues.purpose.${row.purpose}`) }}<span v-if="row.is_reversal" class="text-xs text-muted-foreground"> · {{ t('inventory.issues.reversal') }}</span></template>
      <template #cell-department_name="{ row }">{{ row.department_name ?? '—' }}</template>
      <template #cell-total_value="{ row }">{{ row.held_lines > 0 ? t('inventory.issues.heldShort', { n: row.held_lines }) : formatMoney(row.total_value) }}</template>
      <template #cell-status="{ row }"><Badge :variant="row.status === 'POSTED' ? 'success' : 'outline'">{{ t(`inventory.postedStatus.${row.status}`) }}</Badge></template>
    </DataTable>
    <p v-if="loaded" class="muted mt-3 text-xs">{{ t('inventory.issues.correction') }}</p>
  </template>
</template>
