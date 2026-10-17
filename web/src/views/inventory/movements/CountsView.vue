<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryCategory, InventoryCountBrief, InventoryWarehouse } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'
import { countVariant } from '@/utils/movements'

/**
 * The stock counts (docs/architecture/35-inventory.md 5.5, I9, mockup 26): the counts of the property and, for who may count, the start of a new one. Starting freezes the warehouse for the manual movements; the
 * counters work on a phone (the count page), and a manager reviews and approves the result on the desk.
 */
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)

const counts = ref<InventoryCountBrief[]>([])
const warehouses = ref<InventoryWarehouse[]>([])
const categories = ref<InventoryCategory[]>([])
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const starting = ref(false)
const form = reactive({ warehouse_id: 0, scope: 'ALL', category_id: 0, note: '' })
const clientUuid = ref(crypto.randomUUID())
const fieldError = (f: string) => formError.value?.fieldMessage(f)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  try {
    const path = { params: { path: { propertyId } } }
    const [c, w, k] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/inventory/counts', { params: { path: { propertyId }, query: { limit: 100 } } }),
      api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path),
      api.GET('/api/v1/properties/{propertyId}/inventory/categories', path),
    ])
    counts.value = c.data?.data ?? []
    warehouses.value = (w.data?.data ?? []).filter((x) => x.is_active)
    categories.value = k.data?.data ?? []
    if (!form.warehouse_id) form.warehouse_id = warehouses.value[0]?.id ?? 0
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const open = computed(() => counts.value.filter((c) => c.status === 'OPEN' || c.status === 'SUBMITTED'))
const cols = computed<Column<InventoryCountBrief>[]>(() => [
  { key: 'cnt_number', label: t('inventory.counts.number'), class: 'font-medium whitespace-nowrap' },
  { key: 'warehouse_name', label: t('inventory.counts.warehouse') },
  { key: 'scope', label: t('inventory.counts.scope') },
  { key: 'started_at', label: t('inventory.counts.started'), format: 'datetime' },
  { key: 'progress', label: t('inventory.counts.progress'), align: 'right' },
  { key: 'total_variance_value', label: t('inventory.counts.variance'), align: 'right' },
  { key: 'status', label: t('inventory.counts.status') },
])

async function start(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/counts', {
      params: { path: { propertyId } },
      body: { client_uuid: clientUuid.value, warehouse_id: form.warehouse_id, scope: form.scope as 'ALL', category_id: form.scope === 'CATEGORY' ? form.category_id || null : null, note: form.note.trim() || null },
    })
    if (data) await router.push(`/inventory/counts/${data.id}`)
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <PageHeader :title="t('inventory.counts.title')" :description="t('inventory.counts.intro')">
    <template #actions>
      <Button v-if="can('inventory.count')" type="button" data-testid="count-start-open" @click="starting = !starting">{{ t('inventory.counts.start') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <template v-else>
    <p v-for="c in open" :key="c.id" class="alert warning" :data-testid="`frozen-${c.cnt_number}`">
      {{ t('inventory.counts.frozen', { number: c.cnt_number, warehouse: c.warehouse_name }) }}
      <RouterLink :to="`/inventory/counts/${c.id}`" class="font-medium text-primary hover:underline">{{ t('inventory.counts.open') }}</RouterLink>
    </p>

    <form v-if="starting" novalidate class="mb-4 flex flex-col gap-3 rounded-xl border border-border bg-card p-4" data-testid="count-form" @submit.prevent="start">
      <h2 class="m-0 text-base font-semibold">{{ t('inventory.counts.startTitle') }}</h2>
      <p class="muted m-0 text-sm">{{ t('inventory.counts.startHint') }}</p>
      <ErrorNotice v-if="formError" :error="formError" inline data-testid="count-error" />
      <div class="grid gap-3 sm:grid-cols-3">
        <FormField :label="t('inventory.counts.warehouse')" :error="fieldError('warehouse_id')" required>
          <template #default="{ id, invalid }"><NativeSelect :id="id" v-model.number="form.warehouse_id" name="warehouse_id" :aria-invalid="invalid"><option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option></NativeSelect></template>
        </FormField>
        <FormField :label="t('inventory.counts.scopeLabel')" :error="fieldError('scope')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="form.scope" name="scope"><option value="ALL">{{ t('inventory.counts.scopeAll') }}</option><option value="CATEGORY">{{ t('inventory.counts.scopeCategory') }}</option></NativeSelect>
          </template>
        </FormField>
        <FormField v-if="form.scope === 'CATEGORY'" :label="t('inventory.counts.category')" :error="fieldError('category_id')" required>
          <template #default="{ id }"><NativeSelect :id="id" v-model.number="form.category_id" name="category_id"><option :value="0">—</option><option v-for="c in categories" :key="c.id" :value="c.id">{{ c.code }} · {{ c.name }}</option></NativeSelect></template>
        </FormField>
      </div>
      <FormField :label="t('inventory.request.note')" :error="fieldError('note')"><template #default="{ id }"><Input :id="id" v-model="form.note" name="note" maxlength="300" /></template></FormField>
      <div class="flex justify-end gap-2">
        <Button type="button" variant="outline" @click="starting = false">{{ t('common.cancel') }}</Button>
        <Button type="submit" :disabled="busy || form.warehouse_id === 0 || (form.scope === 'CATEGORY' && form.category_id === 0)" data-testid="count-start">{{ t('inventory.counts.startAndFreeze') }}</Button>
      </div>
    </form>

    <DataTable :columns="cols" :rows="counts" row-key="id" :loading="!loaded" :empty-title="t('inventory.counts.empty')" :empty-description="t('emptyState.inventoryCounts')"
      :row-to="(r) => `/inventory/counts/${r.id}`" :row-test-id="(r) => `count-${r.cnt_number}`" data-testid="counts-table">
      <template #cell-cnt_number="{ row }"><RouterLink :to="`/inventory/counts/${row.id}`" class="text-primary hover:underline">{{ row.cnt_number }}</RouterLink></template>
      <template #cell-scope="{ row }">{{ t(`inventory.counts.scopeName.${row.scope}`) }}</template>
      <template #cell-progress="{ row }">{{ row.counted_count }} / {{ row.line_count }}</template>
      <template #cell-total_variance_value="{ row }">{{ row.total_variance_value ? formatMoney(row.total_variance_value) : '—' }}</template>
      <template #cell-status="{ row }"><Badge :variant="countVariant(row.status)">{{ t(`inventory.countStatus.${row.status}`) }}</Badge></template>
    </DataTable>
  </template>
</template>
