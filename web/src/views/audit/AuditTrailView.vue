<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AuditLog } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const rows = ref<AuditLog[]>([])
const nextCursor = ref<string | undefined>()
const error = ref<ApiError | null>(null)
const loading = ref(false)
const loaded = ref(false)
const open = ref<number | null>(null)
const filter = reactive({ entity_type: '', entity_id: '', user_id: '', action: '', from: '', to: '' })

const columns = computed<Column<AuditLog>[]>(() => [
  { key: 'created_at', label: t('audit.when') },
  { key: 'business_date', label: t('audit.businessDate') },
  { key: 'user', label: t('audit.user') },
  { key: 'action', label: t('audit.action') },
  { key: 'entity', label: t('audit.entity') },
  { key: 'toggle', label: '' },
])

const pid = computed(() => property.currentId)
const allowed = computed(() => auth.can('audit.read', pid.value))

function query(cursor?: string): Record<string, string | number | undefined> {
  const q: Record<string, string | number | undefined> = { limit: 50, cursor }
  for (const [k, v] of Object.entries(filter)) {
    if (v.trim()) q[k] = k === 'entity_id' || k === 'user_id' ? Number(v) : v.trim()
  }
  return q
}

async function load(more = false): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !allowed.value) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/audit-logs', {
      params: { path: { propertyId }, query: query(more ? nextCursor.value : undefined) },
    })
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
    loaded.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

function reset(): void {
  Object.assign(filter, { entity_type: '', entity_id: '', user_id: '', action: '', from: '', to: '' })
  void load()
}

const pretty = (v: unknown): string => (v === null || v === undefined ? '—' : JSON.stringify(v, null, 2))
const time = (iso: string): string => iso.replace('T', ' ').replace(/\.\d+Z$/, 'Z')

watch(pid, () => {
  rows.value = []
  loaded.value = false
  open.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('audit.title')" />

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!allowed" class="muted" data-testid="no-access">{{ t('audit.noAccess', { permission: 'audit.read' }) }}</p>

  <template v-else>
    <Card class="mb-4">
      <form class="grid gap-4 p-4 sm:grid-cols-2 lg:grid-cols-4" novalidate data-testid="filters" @submit.prevent="load()">
        <FormField :label="t('audit.entity')"><template #default="{ id }"><Input :id="id" v-model="filter.entity_type" name="entity_type" :placeholder="t('audit.entityPlaceholder')" /></template></FormField>
        <FormField :label="t('audit.entityId')"><template #default="{ id }"><Input :id="id" v-model="filter.entity_id" name="entity_id" inputmode="numeric" /></template></FormField>
        <FormField :label="t('audit.action')"><template #default="{ id }"><Input :id="id" v-model="filter.action" name="action" placeholder="stay.checked_in" /></template></FormField>
        <FormField :label="t('audit.userId')"><template #default="{ id }"><Input :id="id" v-model="filter.user_id" name="user_id" inputmode="numeric" /></template></FormField>
        <FormField :label="t('audit.from')"><template #default="{ id }"><Input :id="id" v-model="filter.from" name="from" type="date" /></template></FormField>
        <FormField :label="t('audit.to')"><template #default="{ id }"><Input :id="id" v-model="filter.to" name="to" type="date" /></template></FormField>
        <div class="flex items-end gap-2">
          <Button type="submit" :disabled="loading" data-testid="search">{{ t('audit.search') }}</Button>
          <Button type="button" variant="outline" :disabled="loading" data-testid="reset" @click="reset">{{ t('audit.clear') }}</Button>
        </div>
      </form>
    </Card>

    <Card>
      <EmptyState v-if="loaded && !rows.length" :title="t('audit.empty')" data-testid="empty" />
      <DataTable
        v-else-if="rows.length"
        :columns="columns"
        :rows="rows"
        row-key="id"
        :row-test-id="(r) => `entry-${r.id}`"
        :is-expanded="(r) => open === r.id"
        :detail-test-id="(r) => `detail-${r.id}`"
        :caption="t('audit.title')"
      >
        <template #cell-created_at="{ row }">{{ time(row.created_at) }}</template>
        <template #cell-business_date="{ row }">{{ row.business_date ?? '—' }}</template>
        <template #cell-user="{ row }">{{ row.user?.name ?? t('audit.system') }}</template>
        <template #cell-action="{ row }"><code>{{ row.action }}</code></template>
        <template #cell-entity="{ row }">{{ row.entity_type }} #{{ row.entity_id }}</template>
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
      <div v-if="nextCursor" class="flex justify-center p-3">
        <Button type="button" variant="outline" :disabled="loading" data-testid="more" @click="load(true)">{{ t('audit.more') }}</Button>
      </div>
    </Card>
  </template>
</template>
