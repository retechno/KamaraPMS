<script setup lang="ts">
import FilterBar from '@/components/app/FilterBar.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { labelOf } from '@/i18n/labels'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { AuditLog } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import type { RowAction } from '@/components/app/rowActions'
import { usePagedList } from '@/composables/usePagedList'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { AUDIT_ACTIONS, AUDIT_ENTITIES } from '@/utils/audit'

const auth = useAuthStore()
const property = usePropertyStore()

const open = ref<number | null>(null)
const filter = reactive({ entity_type: '', user_id: '', action: '', document: '', from: '', to: '' })
const users = ref<{ id: number; full_name: string }[]>([])

// The choices of the entity and action filters, in the words of the language of the page.
const byLabel = (a: { label: string }, b: { label: string }): number => a.label.localeCompare(b.label)
const entityOptions = computed(() => AUDIT_ENTITIES.map((value) => ({ value, label: labelOf('auditEntity', value) })).sort(byLabel))
const actionOptions = computed(() => AUDIT_ACTIONS.map((value) => ({ value, label: labelOf('auditAction', value) })).sort(byLabel))

// The people to choose from. Whoever may read the audit trail may not be allowed to list the users: then the filter is left out.
async function loadUsers(): Promise<void> {
  try {
    const { data } = await api.GET('/api/v1/users', { params: { query: { limit: 200 } } })
    users.value = (data?.data ?? []).map((u) => ({ id: u.id, full_name: u.full_name })).sort((a, b) => a.full_name.localeCompare(b.full_name))
  } catch {
    users.value = []
  }
}

// The document number is the search (it is in the bar of a phone); the others are filters.
const activeFilters = computed(() => (['entity_type', 'user_id', 'action', 'from', 'to'] as const).filter((k) => filter[k] !== '').length)

const columns = computed<Column<AuditLog>[]>(() => [
  { key: 'created_at', label: t('audit.when'), format: 'datetime' as const, card: 'secondary' as const },
  { key: 'business_date', label: t('audit.businessDate'), format: 'date' as const, hideOnMobile: true },
  { key: 'user', label: t('audit.user') },
  { key: 'action', label: t('audit.action'), card: 'primary' as const },
  { key: 'entity', label: t('audit.entity') },
  { key: 'toggle', label: '', hideOnMobile: true },
])

const pid = computed(() => property.currentId)
const allowed = computed(() => auth.can('audit.read', pid.value))

// The document number is a search of the server: it finds a part of the label of an entry (a number or a code, "0012" in RES000012), in any case, from three characters. Under three
// the search is not sent (the field says so) and the list is as it was.
const MIN_SEARCH = 3
const searchText = computed(() => filter.document.trim())
const searchTooShort = computed(() => searchText.value.length > 0 && [...searchText.value].length < MIN_SEARCH)

function query(cursor?: string): Record<string, string | number | undefined> {
  const q: Record<string, string | number | undefined> = { limit: 50, cursor }
  for (const [k, v] of Object.entries(filter)) {
    if (k !== 'document' && v.trim()) q[k] = k === 'user_id' ? Number(v) : v.trim()
  }
  if (searchText.value && !searchTooShort.value) q.q = searchText.value
  return q
}

async function fetchPage(propertyId: number, cursor?: string) {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/audit-logs', { params: { path: { propertyId }, query: query(cursor) } })
  return { entries: data?.data ?? [], next: data?.next_cursor }
}

// One page of 50 at a time, newest first; every filter, the search too, is asked of the server.
const list = usePagedList<AuditLog>(async (cursor) => {
  const page = await fetchPage(pid.value!, cursor)
  return { data: page.entries, next_cursor: page.next }
})
const { rows, error, loading, loadingMore, loaded, hasMore } = list

async function load(): Promise<void> {
  if (pid.value === null || !allowed.value) return
  await list.reload()
}

/** The details of an entry are opened and closed from its row (a button in the table, the main action of a card). */
const toggleDetails = (id: number): void => {
  open.value = open.value === id ? null : id
}
const actionsOf = (row: AuditLog): RowAction[] => [{ key: 'details', label: open.value === row.id ? t('audit.hide') : t('audit.details'), primary: true, testId: `toggle-${row.id}`, onSelect: () => toggleDetails(row.id) }]

function reset(): void {
  Object.assign(filter, { entity_type: '', user_id: '', action: '', document: '', from: '', to: '' })
  void load()
}

const pretty = (v: unknown): string => (v === null || v === undefined ? '—' : JSON.stringify(v, null, 2))
// What an entry is about: the kind of thing in the words of the page and the label the server wrote ("Room 305", "Reservation RES000012"). An entry written before the label existed
// (or one with no readable number) has none: then its own number or code in what it recorded, and last its id.
const NAMING = ['room_number', 'stay_number', 'payment_number', 'shift_number', 'journal_number', 'item_number', 'request_number', 'confirmation_number', 'folio_number', 'code', 'name']
function entityName(row: { entity_type: string; entity_id: number; entity_label?: string | null; old_data?: unknown; new_data?: unknown }): string {
  const label = labelOf('auditEntity', row.entity_type)
  if (row.entity_label) return `${label} ${row.entity_label}`
  for (const data of [row.new_data, row.old_data]) {
    if (!data || typeof data !== 'object') continue
    const named = NAMING.map((k) => (data as Record<string, unknown>)[k]).find((v) => typeof v === 'string' && v !== '')
    if (named) return `${label} ${named}`
  }
  return `${label} #${row.entity_id}`
}

watch(pid, () => {
  list.reset()
  open.value = null
  void load()
  if (allowed.value) void loadUsers()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('audit.title')" />

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!allowed" class="muted" data-testid="no-access">{{ t('audit.noAccess', { permission: 'audit.read' }) }}</p>

  <template v-else>
    <Card class="mb-4">
      <FilterBar class="grid gap-x-4 gap-y-6 px-4 pb-8 pt-4 sm:grid-cols-2 lg:grid-cols-4" data-testid="filters" :active="activeFilters" @submit="load">
      <template #search>
        <FormField :label="t('audit.document')" :hint="t('audit.documentHint')" :error="searchTooShort ? t('audit.documentTooShort') : undefined">
          <template #default="{ id, invalid }"><Input :id="id" v-model="filter.document" name="document" :placeholder="t('audit.documentPlaceholder')" :aria-invalid="invalid" autocomplete="off" /></template>
        </FormField>
      </template>

        <FormField :label="t('audit.entity')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="filter.entity_type" name="entity_type">
              <option value="">{{ t('audit.allEntities') }}</option>
              <option v-for="o in entityOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('audit.action')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="filter.action" name="action">
              <option value="">{{ t('audit.allActions') }}</option>
              <option v-for="o in actionOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField v-if="users.length" :label="t('audit.user')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="filter.user_id" name="user_id">
              <option value="">{{ t('audit.allUsers') }}</option>
              <option v-for="u in users" :key="u.id" :value="String(u.id)">{{ u.full_name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        
        <FormField float-hint :hint="$weekday(filter.from)" :label="t('audit.from')"><template #default="{ id }"><Input :id="id" v-model="filter.from" name="from" type="date" /></template></FormField>
        <FormField float-hint :hint="$weekday(filter.to)" :label="t('audit.to')"><template #default="{ id }"><Input :id="id" v-model="filter.to" name="to" type="date" /></template></FormField>
      <template #actions>
        <div class="flex items-end gap-2">
          <Button type="submit" :disabled="loading" data-testid="search">{{ t('audit.search') }}</Button>
          <Button type="button" variant="outline" :disabled="loading" data-testid="reset" @click="reset">{{ t('audit.clear') }}</Button>
        </div>
      </template>
    </FilterBar>
    </Card>

    <Card>
      <EmptyState v-if="loaded && !rows.length" :description="t('emptyState.auditEntries')" :action-label="activeFilters || filter.document ? t('dataTable.clearFilters') : ''" action-variant="outline" @action="reset" :title="t('audit.empty')" data-testid="empty" />
      <DataTable
        v-else-if="rows.length"
        :columns="columns"
        :rows="rows"
        row-key="id"
        :has-more="hasMore"
        :loading-more="loadingMore"
        cards
        :row-actions="actionsOf"
        :row-test-id="(r) => `entry-${r.id}`"
        :is-expanded="(r) => open === r.id"
        :detail-test-id="(r) => `detail-${r.id}`"
        :caption="t('audit.title')"
        @load-more="list.loadMore()"
      >
        <template #cell-created_at="{ row }">{{ $dateTime(row.created_at) }}</template>
        <template #cell-business_date="{ row }">{{ $date(row.business_date) || '—' }}</template>
        <template #cell-user="{ row }">{{ row.user?.name ?? t('audit.system') }}</template>
        <template #cell-action="{ row }"><span :title="row.action">{{ labelOf('auditAction', row.action) }}</span></template>
        <template #cell-entity="{ row }">{{ entityName(row) }}</template>
        <template #cell-toggle="{ row }">
          <Button type="button" variant="outline" size="sm" :data-testid="`toggle-${row.id}`" :aria-expanded="open === row.id" @click="open = open === row.id ? null : row.id">{{ open === row.id ? t('audit.hide') : t('audit.details') }}</Button>
        </template>
        <template #detail="{ row }">
          <div class="grid gap-3 sm:grid-cols-2">
            <div><h3 class="m-0 mb-1 text-xs font-semibold uppercase text-muted-foreground">{{ t('audit.before') }}</h3><pre class="m-0 max-h-64 overflow-auto whitespace-pre-wrap break-words text-xs">{{ pretty(row.old_data) }}</pre></div>
            <div><h3 class="m-0 mb-1 text-xs font-semibold uppercase text-muted-foreground">{{ t('audit.after') }}</h3><pre class="m-0 max-h-64 overflow-auto whitespace-pre-wrap break-words text-xs">{{ pretty(row.new_data) }}</pre></div>
          </div>
          <small class="mt-2 block text-muted-foreground">{{ t('audit.request', { id: row.request_id ?? '—' }) }}<template v-if="row.ip_address"> · {{ row.ip_address }}</template></small>
        </template>
      </DataTable>
    </Card>
  </template>
</template>
