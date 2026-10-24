<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetEvent, BanquetEventRow, BanquetEventType } from '@/api/types'
import BanquetEventDialog from '@/components/banquet/BanquetEventDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDateShort } from '@/utils/format'

/** The events of the property (docs/architecture/36-banquet.md 6.3): filtered by status, kind and text; a tentative event whose option date has passed is marked. */
const property = usePropertyStore()
const auth = useAuthStore()
const router = useRouter()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('banquet.manage', pid.value))
const rows = ref<BanquetEventRow[]>([])
const types = ref<BanquetEventType[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const filters = ref({ status: '', event_type_id: 0, q: '', overdue: false })
const dialog = ref(false)

const STATUSES = ['INQUIRY', 'TENTATIVE', 'DEFINITE', 'COMPLETED', 'CANCELLED', 'LOST'] as const
const variant = (s: string): 'draft' | 'booked' | 'inhouse' | 'closed' | 'cancelled' | 'outline' =>
  ({ INQUIRY: 'draft', TENTATIVE: 'booked', DEFINITE: 'inhouse', COMPLETED: 'closed', CANCELLED: 'cancelled', LOST: 'cancelled' } as const)[s as 'INQUIRY'] ?? 'outline'

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  try {
    const f = filters.value
    const [list, ty] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/banquet/events', {
        params: { path: { propertyId }, query: { status: f.status || undefined, event_type_id: f.event_type_id || undefined, q: f.q.trim() || undefined, option_overdue: f.overdue || undefined, limit: 200 } },
      }),
      types.value.length ? Promise.resolve(null) : api.GET('/api/v1/properties/{propertyId}/banquet/event-types', { params: { path: { propertyId } } }),
    ])
    rows.value = list.data?.data ?? []
    if (ty?.data) types.value = ty.data.data
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, () => filters.value.status, () => filters.value.event_type_id, () => filters.value.overdue], load, { immediate: true })

const columns = computed<Column<BanquetEventRow>[]>(() => [
  { key: 'number', label: t('banquet.event.number'), class: 'font-medium' },
  { key: 'name', label: t('banquet.event.name') },
  { key: 'booker_name', label: t('banquet.event.booker') },
  { key: 'first_date', label: t('banquet.event.dates') },
  { key: 'status', label: t('banquet.event.status') },
  { key: 'sales_name', label: t('banquet.event.sales') },
])
const clear = (): void => {
  filters.value = { status: '', event_type_id: 0, q: '', overdue: false }
}
async function onSaved(ev: BanquetEvent): Promise<void> {
  await router.push(`/banquet/events/${ev.id}`)
}
</script>

<template>
  <PageHeader :title="t('banquet.events.title')" :description="t('banquet.events.intro')">
    <template #actions>
      <Button v-if="canManage" type="button" data-testid="event-new" @click="dialog = true">{{ t('banquet.event.new') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <form class="mb-3 flex flex-wrap items-end gap-2" data-testid="event-filters" @submit.prevent="load">
      <NativeSelect v-model="filters.status" name="status" class="w-auto" :aria-label="t('banquet.event.status')" data-testid="filter-status">
        <option value="">{{ t('banquet.events.allStatuses') }}</option>
        <option v-for="s in STATUSES" :key="s" :value="s">{{ t(`banquet.status.${s}`) }}</option>
      </NativeSelect>
      <NativeSelect v-model.number="filters.event_type_id" name="event_type_id" class="w-auto" :aria-label="t('banquet.event.type')">
        <option :value="0">{{ t('banquet.events.allTypes') }}</option>
        <option v-for="x in types" :key="x.id" :value="x.id">{{ x.name }}</option>
      </NativeSelect>
      <Input v-model="filters.q" name="q" class="w-48" :placeholder="t('banquet.events.search')" :aria-label="t('banquet.events.search')" />
      <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="filters.overdue" type="checkbox" name="overdue" class="size-4 accent-primary" data-testid="filter-overdue" />{{ t('banquet.events.overdueOnly') }}</label>
      <Button type="submit" variant="outline">{{ t('common.search') }}</Button>
    </form>
    <DataTable
      :columns="columns" :rows="rows" row-key="id" :loading="!loaded" :empty-title="t('banquet.events.empty')" :empty-description="t('emptyState.banquetEvents')"
      :row-to="(r) => `/banquet/events/${r.id}`" :row-test-id="(r) => `event-${r.number}`" data-testid="events-table"
    >
      <template #cell-number="{ row }"><RouterLink :to="`/banquet/events/${row.id}`" class="text-primary hover:underline">{{ row.number }}</RouterLink></template>
      <template #cell-first_date="{ row }">
        <template v-if="row.first_date">{{ formatDateShort(row.first_date) }}<template v-if="row.last_date && row.last_date !== row.first_date"> – {{ formatDateShort(row.last_date) }}</template></template>
        <template v-else>–</template>
      </template>
      <template #cell-status="{ row }">
        <Badge :variant="variant(row.status)">{{ t(`banquet.status.${row.status}`) }}</Badge>
        <Badge v-if="row.option_overdue" variant="destructive" class="ml-1" data-testid="option-overdue">{{ t('banquet.event.optionOverdue') }}</Badge>
      </template>
    </DataTable>
    <p v-if="loaded && !rows.length && (filters.status || filters.q || filters.overdue || filters.event_type_id)" class="mt-2"><Button type="button" variant="outline" @click="clear">{{ t('banquet.events.clear') }}</Button></p>
  </template>
  <BanquetEventDialog v-if="pid !== null" v-model:open="dialog" :property-id="pid" :types="types" @saved="onSaved" />
</template>
