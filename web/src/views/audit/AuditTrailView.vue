<script setup lang="ts">
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
const notFound = ref(false)
const scanCapped = ref(false)

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

// A document number is looked for in two ways. A reservation number (RES…) is turned into the reservation it names, and the server filters on that. For the
// other documents the entries are read page by page and the ones that carry the number are kept: the server cannot search for it yet.
const RESERVATION_NUMBER = /^RES\d+$/
const SCAN_PAGES = 10
const SCAN_ENOUGH = 20
let reservation: number | null = null

function query(cursor?: string): Record<string, string | number | undefined> {
  const q: Record<string, string | number | undefined> = { limit: 50, cursor }
  for (const [k, v] of Object.entries(filter)) {
    if (k !== 'document' && v.trim()) q[k] = k === 'user_id' ? Number(v) : v.trim()
  }
  if (reservation !== null) Object.assign(q, { entity_type: 'reservation', entity_id: reservation })
  return q
}

const carries = (row: AuditLog, number: string): boolean => JSON.stringify([row.old_data, row.new_data]).toUpperCase().includes(number)

async function fetchPage(propertyId: number, cursor?: string) {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/audit-logs', { params: { path: { propertyId }, query: query(cursor) } })
  return { entries: data?.data ?? [], next: data?.next_cursor }
}

async function findReservation(propertyId: number, number: string): Promise<number | null> {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations', { params: { path: { propertyId }, query: { q: number, limit: 5 } } })
  return data?.data?.find((r) => r.confirmation_number === number)?.id ?? null
}

// One page of 50 at a time. A reservation number is the reservation it names (asked of the server); another document number is looked for in the entries, page after
// page, and what carries it is the page that is given back (see above).
const list = usePagedList<AuditLog>(async (cursor) => {
  const propertyId = pid.value!
  const number = filter.document.trim().toUpperCase()
  if (!cursor) {
    notFound.value = false
    reservation = null
    if (RESERVATION_NUMBER.test(number)) {
      reservation = await findReservation(propertyId, number)
      if (reservation === null) {
        notFound.value = true
        return { data: [] }
      }
    }
  }
  if (number && reservation === null) {
    let next = cursor
    const found: AuditLog[] = []
    let pages = 0
    do {
      const page = await fetchPage(propertyId, next)
      found.push(...page.entries.filter((e) => carries(e, number)))
      next = page.next
      pages++
    } while (next && found.length < SCAN_ENOUGH && pages < SCAN_PAGES)
    scanCapped.value = !!next && found.length < SCAN_ENOUGH
    return { data: found, next_cursor: next }
  }
  scanCapped.value = false
  const page = await fetchPage(propertyId, cursor)
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
// What names the thing an entry is about: its own number or code when the entry carries one ("Stay STY000035"), otherwise its id.
const NAMING = ['room_number', 'stay_number', 'payment_number', 'shift_number', 'journal_number', 'item_number', 'request_number', 'confirmation_number', 'folio_number', 'code', 'name']
function entityName(row: { entity_type: string; entity_id: number; old_data?: unknown; new_data?: unknown }): string {
  const label = labelOf('auditEntity', row.entity_type)
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
      <form class="grid gap-x-4 gap-y-6 px-4 pb-8 pt-4 sm:grid-cols-2 lg:grid-cols-4" novalidate data-testid="filters" @submit.prevent="load()">
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
        <FormField :label="t('audit.document')">
          <template #default="{ id }"><Input :id="id" v-model="filter.document" name="document" :placeholder="t('audit.documentPlaceholder')" autocomplete="off" /></template>
        </FormField>
        <FormField float-hint :hint="$weekday(filter.from)" :label="t('audit.from')"><template #default="{ id }"><Input :id="id" v-model="filter.from" name="from" type="date" /></template></FormField>
        <FormField float-hint :hint="$weekday(filter.to)" :label="t('audit.to')"><template #default="{ id }"><Input :id="id" v-model="filter.to" name="to" type="date" /></template></FormField>
        <div class="flex items-end gap-2">
          <Button type="submit" :disabled="loading" data-testid="search">{{ t('audit.search') }}</Button>
          <Button type="button" variant="outline" :disabled="loading" data-testid="reset" @click="reset">{{ t('audit.clear') }}</Button>
        </div>
      </form>
    </Card>

    <Card>
      <EmptyState v-if="loaded && !rows.length" :title="notFound ? t('audit.documentNotFound', { number: filter.document.trim().toUpperCase() }) : t('audit.empty')" data-testid="empty" />
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
      <p v-if="scanCapped" class="m-0 px-4 pt-3 text-sm text-muted-foreground" data-testid="scan-capped">{{ t('audit.scanCapped') }}</p>
    </Card>
  </template>
</template>
