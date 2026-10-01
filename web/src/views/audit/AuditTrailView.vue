<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AuditLog } from '@/api/types'
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
  <div class="page-head">
    <h1 class="page-title">Audit trail</h1>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!allowed" class="muted" data-testid="no-access">Your role at this property does not allow reading the audit trail (the <code>audit.read</code> permission).</p>

  <template v-else>
    <form class="card filters" novalidate data-testid="filters" @submit.prevent="load()">
      <label class="field"><span>Entity</span><input v-model="filter.entity_type" name="entity_type" placeholder="stay, folio, tax…" /></label>
      <label class="field"><span>Entity id</span><input v-model="filter.entity_id" name="entity_id" inputmode="numeric" /></label>
      <label class="field"><span>Action</span><input v-model="filter.action" name="action" placeholder="stay.checked_in" /></label>
      <label class="field"><span>User id</span><input v-model="filter.user_id" name="user_id" inputmode="numeric" /></label>
      <label class="field"><span>From</span><input v-model="filter.from" name="from" type="date" /></label>
      <label class="field"><span>To</span><input v-model="filter.to" name="to" type="date" /></label>
      <button type="submit" class="btn-primary" :disabled="loading" data-testid="search">Search</button>
      <button type="button" :disabled="loading" data-testid="reset" @click="reset">Clear</button>
    </form>

    <section class="card">
      <p v-if="loaded && !rows.length" class="muted" data-testid="empty">No entries match.</p>
      <table v-else-if="rows.length" class="list">
        <thead><tr><th>When</th><th>Business date</th><th>User</th><th>Action</th><th>Entity</th><th /></tr></thead>
        <tbody>
          <template v-for="r in rows" :key="r.id">
            <tr :data-testid="`entry-${r.id}`">
              <td>{{ time(r.created_at) }}</td>
              <td>{{ r.business_date ?? '—' }}</td>
              <td>{{ r.user?.name ?? 'system' }}</td>
              <td><code>{{ r.action }}</code></td>
              <td>{{ r.entity_type }} #{{ r.entity_id }}</td>
              <td><button type="button" :data-testid="`toggle-${r.id}`" :aria-expanded="open === r.id" @click="open = open === r.id ? null : r.id">{{ open === r.id ? 'Hide' : 'Details' }}</button></td>
            </tr>
            <tr v-if="open === r.id" :data-testid="`detail-${r.id}`">
              <td colspan="6">
                <div class="diff">
                  <div><h3>Before</h3><pre>{{ pretty(r.old_data) }}</pre></div>
                  <div><h3>After</h3><pre>{{ pretty(r.new_data) }}</pre></div>
                </div>
                <small class="muted">Request {{ r.request_id ?? '—' }}<template v-if="r.ip_address"> · {{ r.ip_address }}</template></small>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
      <div v-if="nextCursor" class="form-actions">
        <button type="button" :disabled="loading" data-testid="more" @click="load(true)">Load more</button>
      </div>
    </section>
  </template>
</template>

<style scoped>
.filters {
  display: flex;
  gap: 12px;
  align-items: flex-end;
  flex-wrap: wrap;
}
.list {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
}
.list th,
.list td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--border);
  vertical-align: top;
}
.diff {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 12px;
}
pre {
  margin: 0;
  max-height: 260px;
  overflow: auto;
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
